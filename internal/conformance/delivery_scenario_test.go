package conformance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	layer "github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The delivery policy (V4-33) finds people and their accounts through the same fake database.

func (d *memoryDB) PersonOfKey(_ context.Context, key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.personOfKeyLocked(key), nil
}

func (d *memoryDB) PersonOfAccount(_ context.Context, channelName, externalID string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.accounts[accountKey(channelName, externalID)]; ok {
		return a.PersonID, nil
	}
	return "", nil
}

// lastWrote sets when the account last wrote.
func (d *memoryDB) lastWrote(channelName, externalID string, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.accounts[accountKey(channelName, externalID)]
	if !ok {
		panic("no account " + accountKey(channelName, externalID))
	}
	a.LastInboundAt = &at
}

// deliveryOf returns the recorded delivery of the person behind the key.
func (e *env) deliveryOf(p person) models.ProactiveDelivery {
	e.t.Helper()
	u, ok := e.db.user(p.UserID)
	require.True(e.t, ok, "no user %s", p.UserID)
	var found []models.ProactiveDelivery
	for _, d := range e.deliveries.All() {
		if d.PersonID == u.ID {
			found = append(found, d)
		}
	}
	require.Len(e.t, found, 1, "one recorded delivery for %s", p.UserID)
	return found[0]
}

// reminderOutsideWindow is V4-33's "done when", on every adapter in every mode: a reminder that
// falls due after the person last wrote on the channel is delivered by one of the policy's routes
// and never silently lost. Which route is read from the adapter's declared rule, never from its
// name: inside the window it goes as it is; outside it, as the approved template where the
// channel has one, else on the person's other channel when that is open, else it is held and
// goes out, marked late, when the person next writes.
//
// Three people: one who uses only this channel and wrote 30 hours ago; one who also has the other
// channel, written an hour ago; and one like the second whose last message here was five days ago
// (past a sandbox membership, ADR 0003 §4).
func reminderOutsideWindow(t *testing.T, e *env) {
	rule := e.caps().Rule
	other := e.otherChannel()
	now := time.Now()

	solo := e.member(1, "family", "Levi")
	both := e.member(2, "family", "Levi")
	lapsed := e.member(3, "family", "Levi")
	ago := map[string]time.Duration{solo.UserID: 30 * time.Hour, both.UserID: 30 * time.Hour, lapsed.UserID: 5 * 24 * time.Hour}
	for _, p := range []person{solo, both, lapsed} {
		e.db.lastWrote(e.ch.name, p.UserID, now.Add(-ago[p.UserID]))
	}
	elsewhere := map[string]person{both.UserID: other.person(8), lapsed.UserID: other.person(9)}
	for owner, there := range elsewhere {
		u, _ := e.db.user(owner)
		e.db.addAccount(other.name, there.UserID, u.ID, now.Add(-48*time.Hour)) // the preferred account stays this channel's
		e.db.lastWrote(other.name, there.UserID, now.Add(-time.Hour))
	}

	past := now.Add(-time.Minute)
	for _, p := range []person{solo, both, lapsed} {
		require.NoError(t, e.db.SaveReminder(models.ScheduledReminder{UserID: p.UserID, Message: "Call the dentist about **Noa**'s appointment", DueTime: past}))
	}
	e.engine.CheckAndSendReminders()

	e.db.mu.Lock()
	for i, r := range e.db.reminders {
		assert.True(t, r.IsSent, "reminder %d was handed to the delivery policy", i+1)
	}
	e.db.mu.Unlock()

	// The expected route of each person, from the declared rule alone.
	expect := func(p person) (state, route, channelName string) {
		switch rule.At(now.Add(-ago[p.UserID]), now) {
		case layer.Open:
			return models.DeliveryDelivered, models.RouteDirect, e.ch.name
		case layer.Closed:
			if rule.HasTemplate(layer.TemplateReminder) {
				return models.DeliveryDelivered, models.RouteTemplate, e.ch.name
			}
		}
		if _, ok := elsewhere[p.UserID]; ok {
			return models.DeliveryMoved, models.RouteOther, other.name
		}
		return models.DeliveryHeld, "", ""
	}
	held := false
	for _, p := range []person{solo, both, lapsed} {
		d := e.deliveryOf(p)
		state, route, channelName := expect(p)
		assert.Equal(t, state, d.State, "%s: outcome", p.UserID)
		assert.Equal(t, route, d.Route, "%s: route", p.UserID)
		assert.Equal(t, channelName, d.Channel, "%s: channel", p.UserID)
		held = held || state == models.DeliveryHeld
	}

	if held {
		// The person writes again: the held reminder goes out first, marked late, then the answer.
		e.model.script(say("Hello!"))
		e.says(solo, "Hello again")
		d := e.deliveryOf(solo)
		assert.Equal(t, models.DeliveryLate, d.State)
		assert.Equal(t, models.RouteOnReply, d.Route)
		texts := e.texts()
		require.GreaterOrEqual(t, len(texts), 2)
		assert.Contains(t, texts[len(texts)-2], "when I could not reach you")
	}

	// Never silently lost: every reminder ended in a recorded outcome.
	for _, d := range e.deliveries.All() {
		assert.Contains(t, []string{models.DeliveryDelivered, models.DeliveryMoved, models.DeliveryLate}, d.State)
	}
	// The note says when the reminder was due, which is a minute before the run.
	e.golden("reminder_outside_window", regexp.MustCompile(`(This was due )[^,]+, \d\d:\d\d`))
}

// A refusal WhatsApp reports later, after Twilio accepted the message, travels the production
// path (the adapter's signed status route, the queue payload, the engine) to the delivery policy
// and reopens the message (V4-33, ADR 0003 §4: Twilio's clock is the truth). MyPA thought the
// window open, 23 hours after the person wrote; Twilio answers 63016. In sender mode the template
// follows; in the sandbox, which has none, the reminder is held for the person's next message.
func TestDeliveryReport_ReopensTheMessageInEachMode(t *testing.T) {
	ch, ok := fixtureFor(twilio.Name)
	require.True(t, ok)
	for _, mode := range ch.modeNames() {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t, ch, mode)
			p := e.member(1, "family", "Levi")
			e.db.lastWrote(ch.name, p.UserID, time.Now().Add(-23*time.Hour))
			require.NoError(t, e.db.SaveReminder(models.ScheduledReminder{UserID: p.UserID, Message: "Water the plants", DueTime: time.Now().Add(-time.Minute)}))
			e.engine.CheckAndSendReminders()
			first := e.deliveryOf(p)
			require.Equal(t, models.RouteDirect, first.Route, "inside the window by MyPA's clock")
			require.NotEmpty(t, first.Refs, "the message SID is kept")

			const statusURL = "https://mypa.example" + twilio.StatusCallbackPath
			form := url.Values{"AccountSid": {twilioAccountSID}, "MessageSid": {first.Refs}, "MessageStatus": {"undelivered"},
				"ErrorCode": {"63016"}, "To": {"whatsapp:" + p.UserID}}
			report := func() {
				req := httptest.NewRequest(http.MethodPost, statusURL, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Twilio-Signature", twilioSignature(statusURL, form))
				msg, ok := e.post(req)
				require.True(t, ok, "the report is queued")
				require.NotNil(t, msg.Report)
				require.NoError(t, e.engine.ProcessMessage(context.Background(), msg))
			}
			report()

			got := e.deliveryOf(p)
			assert.Equal(t, string(layer.WindowClosed), got.Reason)
			if e.caps().Rule.HasTemplate(layer.TemplateReminder) {
				assert.Equal(t, models.DeliveryDelivered, got.State)
				assert.Equal(t, models.RouteTemplate, got.Route)
				require.Len(t, e.wire.messages(), 2)
				assert.Contains(t, e.wire.messages()[1].Meta, "content="+reminderTemplateSID)
			} else {
				assert.Equal(t, models.DeliveryHeld, got.State)
				assert.Len(t, e.wire.messages(), 1, "nothing more is sent on hope")
			}

			// Twilio repeats the callback: nothing is reopened twice.
			report()
			assert.Equal(t, got.State, e.deliveryOf(p).State)
			if got.State == models.DeliveryDelivered {
				assert.Len(t, e.wire.messages(), 2)
			} else {
				assert.Len(t, e.wire.messages(), 1)
			}
		})
	}
}
