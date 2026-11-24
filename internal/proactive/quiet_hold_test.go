package proactive_test

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/nivik/mypa/internal/quiet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quietWorld is a world whose person keeps quiet hours 22:00-07:00 in Asia/Jerusalem (UTC+2 in
// November), on a channel that is always open.
func quietWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, channel.ProactiveRule{}, false)
	w.people.wrote("chat", w.now)
	clock := quiet.NewClock(quiet.Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, "")
	w.policy = proactive.New(w.store, w.people, channelsOf(t, w), proactive.WithClock(func() time.Time { return w.now }),
		proactive.WithQuietHours(func(_ context.Context, id string) quiet.Clock {
			require.Equal(t, personID, id, "the clock is read for the recipient")
			return clock
		}))
	// 23:30 in Jerusalem: inside the quiet hours, which end at 07:00 (05:00 UTC) the next day.
	w.now = time.Date(2026, 11, 20, 21, 30, 0, 0, time.UTC)
	return w
}

func channelsOf(t *testing.T, w *world) *channel.Registry {
	t.Helper()
	reg := channel.NewRegistry()
	require.NoError(t, reg.Register(w.chat))
	reg.Seal()
	return reg
}

// BL-49: a notification that respects quiet hours and reaches its person inside them is held in
// its delivery row and goes out when they end; nothing else stores it.
func TestDeliver_ANotificationInQuietHoursWaitsUntilTheyEnd(t *testing.T) {
	w := quietWorld(t)
	d := w.deliver(proactive.Request{Category: proactive.Notification, Text: "📢 Dana logged: groceries", HoldInQuietHours: true})

	assert.Empty(t, w.chat.messages(), "nothing goes out during quiet hours")
	assert.Equal(t, models.DeliveryPending, d.State)
	require.NotNil(t, d.NextAttemptAt)
	end := time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)
	assert.True(t, d.NextAttemptAt.Equal(end), "due when the quiet hours end, got %s", d.NextAttemptAt.UTC())
	assert.Equal(t, "quiet_hours", d.Reason)

	w.now = end.Add(-time.Minute)
	w.policy.Sweep(context.Background())
	assert.Empty(t, w.chat.messages(), "still quiet a minute before the end")

	w.now = end
	w.policy.Sweep(context.Background())
	msgs := w.chat.messages()
	require.Len(t, msgs, 1, "it arrives when the quiet hours end")
	assert.Equal(t, "📢 Dana logged: groceries", msgs[0].Out.Text)
	assert.Equal(t, models.DeliveryDelivered, w.only().State)
}

// An urgent one is not held, and neither is a message that does not ask to respect quiet hours.
func TestDeliver_AnUrgentNotificationIsNotHeld(t *testing.T) {
	w := quietWorld(t)
	w.deliver(proactive.Request{Category: proactive.Notification, Text: "can wait", HoldInQuietHours: true})
	_, err := w.policy.Deliver(proactive.Urgent(context.Background()), proactive.Request{Key: "alice", Category: proactive.Notification, Text: "🚨 urgent", HoldInQuietHours: true})
	require.NoError(t, err)
	w.deliver(proactive.Request{Category: proactive.Notification, Text: "✅ signed in"})

	var texts []string
	for _, m := range w.chat.messages() {
		texts = append(texts, m.Out.Text)
	}
	assert.ElementsMatch(t, []string{"🚨 urgent", "✅ signed in"}, texts)
}

// Outside quiet hours nothing changes.
func TestDeliver_OutsideQuietHoursANotificationGoesAtOnce(t *testing.T) {
	w := quietWorld(t)
	w.now = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC) // 12:00 in Jerusalem
	d := w.deliver(proactive.Request{Category: proactive.Notification, Text: "hi", HoldInQuietHours: true})
	assert.Equal(t, models.DeliveryDelivered, d.State)
	assert.Len(t, w.chat.messages(), 1)
}
