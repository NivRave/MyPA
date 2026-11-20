package proactive_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/nivik/mypa/internal/proactive/proactivetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdapter is a channel with a declared rule. It records what it is asked to send and answers
// with what the test set: accept, refuse, or fail.
type fakeAdapter struct {
	name string
	rule channel.ProactiveRule
	join string

	mu      sync.Mutex
	sent    []sentMessage
	refuse  channel.RefusalReason
	fail    error
	retryAt time.Time
	seq     int
}

type sentMessage struct {
	To  channel.Address
	Out channel.Outbound
	Ref string
}

func (a *fakeAdapter) Name() string { return a.name }
func (a *fakeAdapter) Capabilities() channel.Capabilities {
	return channel.Capabilities{MaxTextLen: 4000, Markup: channel.PlainText, Proactive: "per rule", Rule: a.rule}
}
func (a *fakeAdapter) Ingress() channel.Ingress {
	return channel.Ingress{Outward: &channel.Outward{Run: func(ctx context.Context, _ channel.Sink) error { <-ctx.Done(); return ctx.Err() }}}
}
func (a *fakeAdapter) Fetch(context.Context, channel.AttachmentRef) (io.ReadCloser, error) {
	return nil, errors.New("no files")
}
func (a *fakeAdapter) JoinStep() string { return a.join }

func (a *fakeAdapter) Send(_ context.Context, to channel.Address, m channel.Outbound) (channel.Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return channel.Receipt{}, a.fail
	}
	if a.refuse != "" {
		return channel.Receipt{Reason: a.refuse, RetryAt: a.retryAt}, nil
	}
	a.seq++
	ref := a.name + "-" + string(rune('a'+a.seq))
	a.sent = append(a.sent, sentMessage{To: to, Out: m, Ref: ref})
	return channel.Receipt{Accepted: true, Refs: []string{ref}}, nil
}

func (a *fakeAdapter) messages() []sentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sentMessage(nil), a.sent...)
}

func (a *fakeAdapter) set(refuse channel.RefusalReason, fail error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refuse, a.fail = refuse, fail
}

// fakePeople is one person and their accounts.
type fakePeople struct {
	mu       sync.Mutex
	person   models.Person
	accounts []models.ChannelAccount
}

func (p *fakePeople) PersonOfKey(_ context.Context, key string) (string, error) {
	if key == "alice" || key == p.person.ID {
		return p.person.ID, nil
	}
	return "", nil
}
func (p *fakePeople) PersonOfAccount(_ context.Context, ch, ext string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.Channel == ch && a.ExternalID == ext {
			return a.PersonID, nil
		}
	}
	return "", nil
}
func (p *fakePeople) PersonByID(_ context.Context, id string) (*models.Person, error) {
	if id != p.person.ID {
		return nil, nil
	}
	pp := p.person
	return &pp, nil
}
func (p *fakePeople) AccountsOfPerson(_ context.Context, id string) ([]models.ChannelAccount, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]models.ChannelAccount(nil), p.accounts...), nil
}

// wrote sets when the person last wrote from their account on the channel.
func (p *fakePeople) wrote(ch string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Channel == ch {
			t := at
			p.accounts[i].LastInboundAt = &t
		}
	}
}

// world is one person with an account on a windowed channel ("chat", preferred) and one on an
// always-open channel ("mail").
type world struct {
	t      *testing.T
	now    time.Time
	store  *proactivetest.Store
	people *fakePeople
	chat   *fakeAdapter
	mail   *fakeAdapter
	policy *proactive.Policy
}

const personID = "11111111-1111-1111-1111-111111111111"

func newWorld(t *testing.T, chatRule channel.ProactiveRule, withMail bool) *world {
	t.Helper()
	w := &world{
		t:     t,
		now:   time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
		store: proactivetest.NewStore(),
		chat:  &fakeAdapter{name: "chat", rule: chatRule},
		mail:  &fakeAdapter{name: "mail"},
	}
	if chatRule.Reach > 0 {
		w.chat.join = "Send `join word` to the chat number first."
	}
	reg := channel.NewRegistry()
	require.NoError(t, reg.Register(w.chat))
	require.NoError(t, reg.Register(w.mail))
	reg.Seal()

	preferred := "acct-chat"
	w.people = &fakePeople{
		person:   models.Person{ID: personID, PreferredAccountID: &preferred},
		accounts: []models.ChannelAccount{{ID: "acct-chat", PersonID: personID, Channel: "chat", ExternalID: "+100"}},
	}
	if withMail {
		w.people.accounts = append(w.people.accounts, models.ChannelAccount{ID: "acct-mail", PersonID: personID, Channel: "mail", ExternalID: "m-1"})
	}
	w.policy = proactive.New(w.store, w.people, reg, proactive.WithClock(func() time.Time { return w.now }))
	return w
}

func (w *world) deliver(req proactive.Request) *models.ProactiveDelivery {
	w.t.Helper()
	if req.Key == "" {
		req.Key = "alice"
	}
	d, err := w.policy.Deliver(context.Background(), req)
	require.NoError(w.t, err)
	return d
}

func (w *world) only() models.ProactiveDelivery {
	w.t.Helper()
	rows := w.store.All()
	require.Len(w.t, rows, 1)
	return rows[0]
}

var (
	window  = channel.ProactiveRule{Window: 24 * time.Hour}
	sender  = channel.ProactiveRule{Window: 24 * time.Hour, Templates: []string{channel.TemplateReminder, channel.TemplateBriefing}}
	sandbox = channel.ProactiveRule{Window: 24 * time.Hour, Reach: 72 * time.Hour}
)

func reminder() proactive.Request {
	return proactive.Request{Category: proactive.Reminder, Text: "🔔 Reminder: call the dentist",
		TemplateKey: channel.TemplateReminder, TemplateValues: []string{"call the dentist"}}
}

// Inside the window the message goes to the preferred channel as it is.
func TestDeliver_InsideTheWindowGoesDirect(t *testing.T) {
	w := newWorld(t, sender, true)
	w.people.wrote("chat", w.now.Add(-time.Hour))
	w.people.wrote("mail", w.now.Add(-time.Hour))

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryDelivered, d.State)
	assert.Equal(t, models.RouteDirect, d.Route)
	require.Len(t, w.chat.messages(), 1)
	assert.Equal(t, "🔔 Reminder: call the dentist", w.chat.messages()[0].Out.Text)
	assert.Empty(t, w.chat.messages()[0].Out.Proactive.TemplateKey)
	assert.Empty(t, w.mail.messages())
	assert.Equal(t, w.chat.messages()[0].Ref, w.only().Refs, "the platform's ID is kept to match a later report")
}

// The "done when", route 1 of 3: outside the window, a sender with an approved template sends
// the reminder as that template, before trying another channel (ADR 0003 §11, question 1).
func TestDeliver_OutsideTheWindowUsesTheTemplate(t *testing.T) {
	w := newWorld(t, sender, true)
	w.people.wrote("chat", w.now.Add(-3*24*time.Hour))
	w.people.wrote("mail", w.now.Add(-time.Hour))

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryDelivered, d.State)
	assert.Equal(t, models.RouteTemplate, d.Route)
	require.Len(t, w.chat.messages(), 1)
	p := w.chat.messages()[0].Out.Proactive
	assert.Equal(t, channel.TemplateReminder, p.TemplateKey)
	assert.Equal(t, []string{"call the dentist"}, p.TemplateValues)
	assert.Empty(t, w.mail.messages(), "the template comes first: the person chose that channel")
}

// Route 2 of 3: no template (a sandbox), so the other channel, which is open, gets it.
func TestDeliver_OutsideTheWindowWithoutTemplateMovesToTheOtherChannel(t *testing.T) {
	w := newWorld(t, sandbox, true)
	w.people.wrote("chat", w.now.Add(-48*time.Hour))
	w.people.wrote("mail", w.now.Add(-10*24*time.Hour))

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryMoved, d.State)
	assert.Equal(t, "mail", d.Channel)
	assert.Empty(t, w.chat.messages())
	require.Len(t, w.mail.messages(), 1)
	assert.Equal(t, "🔔 Reminder: call the dentist", w.mail.messages()[0].Out.Text,
		"the membership has not lapsed, so there is nothing to rejoin")
}

// A lapsed sandbox membership is not reachable; the person is told on the other channel how to
// rejoin (ADR 0003 §4).
func TestDeliver_LapsedMembershipMovesWithTheRejoinStep(t *testing.T) {
	w := newWorld(t, sandbox, true)
	w.people.wrote("chat", w.now.Add(-5*24*time.Hour))
	w.people.wrote("mail", w.now.Add(-30*24*time.Hour))

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryMoved, d.State)
	require.Len(t, w.mail.messages(), 1)
	text := w.mail.messages()[0].Out.Text
	assert.True(t, strings.HasPrefix(text, "🔔 Reminder: call the dentist"))
	assert.Contains(t, text, "I could not reach you on chat")
	assert.Contains(t, text, "join word")
}

// Route 3 of 3: nothing is open, so the reminder is held, and goes out with a note that it is
// late when the person next writes, on the account they wrote from. It is sent once.
func TestDeliver_NothingOpenIsHeldUntilThePersonWrites(t *testing.T) {
	w := newWorld(t, sandbox, false)
	w.people.wrote("chat", w.now.Add(-30*time.Hour))
	req := reminder()
	req.DueAt = w.now

	d := w.deliver(req)
	assert.Equal(t, models.DeliveryHeld, d.State)
	assert.Empty(t, w.chat.messages(), "nothing is sent on hope")

	w.now = w.now.Add(5 * time.Hour)
	w.people.wrote("chat", w.now)
	w.policy.Release(context.Background(), "chat", "+100")
	require.Len(t, w.chat.messages(), 1)
	text := w.chat.messages()[0].Out.Text
	assert.Equal(t, "⏰ This was due Fri 20 Nov, 09:00, when I could not reach you.\n\n🔔 Reminder: call the dentist", text)
	got := w.only()
	assert.Equal(t, models.DeliveryLate, got.State)
	assert.Equal(t, models.RouteOnReply, got.Route)
	require.NotNil(t, got.DeliveredAt)

	w.policy.Release(context.Background(), "chat", "+100")
	assert.Len(t, w.chat.messages(), 1, "a released message is not sent twice")
}

// The late note is on the person's clock.
func TestRelease_LateNoteUsesThePersonsZone(t *testing.T) {
	w := newWorld(t, sandbox, false)
	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	reg := channel.NewRegistry()
	require.NoError(t, reg.Register(w.chat))
	w.policy = proactive.New(w.store, w.people, reg, proactive.WithClock(func() time.Time { return w.now }),
		proactive.WithZone(func(context.Context, string) *time.Location { return jerusalem }))
	w.people.wrote("chat", w.now.Add(-30*time.Hour))
	req := reminder()
	req.DueAt = w.now
	w.deliver(req)
	w.policy.Release(context.Background(), "chat", "+100")
	require.Len(t, w.chat.messages(), 1)
	assert.Contains(t, w.chat.messages()[0].Out.Text, "Fri 20 Nov, 11:00")
}

// A briefing's template only says it is waiting, so the briefing itself is held and goes out,
// without a "late" note, when the person replies.
func TestDeliver_BriefingTemplateIsANoticeAndTheTextFollowsTheReply(t *testing.T) {
	w := newWorld(t, sender, false)
	w.people.wrote("chat", w.now.Add(-2*24*time.Hour))
	d := w.deliver(proactive.Request{Category: proactive.Briefing, Text: "☀️ Your day: two meetings.",
		TemplateKey: channel.TemplateBriefing, TemplateValues: []string{"Friday, 20 November"}})
	assert.Equal(t, models.DeliveryHeld, d.State)
	assert.Equal(t, models.RouteTemplate, d.Route)
	require.Len(t, w.chat.messages(), 1)
	assert.Equal(t, channel.TemplateBriefing, w.chat.messages()[0].Out.Proactive.TemplateKey)

	w.people.wrote("chat", w.now)
	w.policy.Release(context.Background(), "chat", "+100")
	require.Len(t, w.chat.messages(), 2)
	assert.Equal(t, "☀️ Your day: two meetings.", w.chat.messages()[1].Out.Text)
	assert.Equal(t, models.DeliveryDelivered, w.only().State)
}

// The prediction can be wrong. A refusal the platform reports later reopens the message, and the
// chain runs again without the refused route: here the template is next.
func TestReport_WindowClosedReopensAndTriesTheNextRoute(t *testing.T) {
	w := newWorld(t, sender, true)
	w.people.wrote("chat", w.now.Add(-23*time.Hour))
	d := w.deliver(reminder())
	require.Equal(t, models.RouteDirect, d.Route)
	ref := w.chat.messages()[0].Ref

	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: ref, Reason: channel.WindowClosed}))
	got := w.only()
	assert.Equal(t, models.DeliveryDelivered, got.State)
	assert.Equal(t, models.RouteTemplate, got.Route)
	assert.Equal(t, string(channel.WindowClosed), got.Reason)
	require.Len(t, w.chat.messages(), 2)
	assert.Equal(t, channel.TemplateReminder, w.chat.messages()[1].Out.Proactive.TemplateKey)

	// The same report again, or one for a reply, matches nothing.
	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: ref, Reason: channel.WindowClosed}))
	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: "not-ours", Reason: channel.NotReachable}))
	assert.Len(t, w.chat.messages(), 2)
}

// A refused template, or an account that turns out unreachable, leaves the other channel, and
// failing that, a hold: the message is never lost.
func TestReport_RefusedEverywhereEndsHeld(t *testing.T) {
	w := newWorld(t, sender, false)
	w.people.wrote("chat", w.now.Add(-2*24*time.Hour))
	w.deliver(reminder())
	require.Equal(t, models.RouteTemplate, w.only().Route)

	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: w.chat.messages()[0].Ref, Reason: channel.NotReachable}))
	got := w.only()
	assert.Equal(t, models.DeliveryHeld, got.State)
	assert.Contains(t, got.Skip, "chat:+100#template")

	w.people.wrote("chat", w.now)
	w.policy.Release(context.Background(), "chat", "+100")
	assert.Equal(t, models.DeliveryLate, w.only().State, "writing again shows the person is reachable")
}

// A send that fails with an error is retried by the minute tick; after three failures the chain
// moves on to the next route.
func TestDeliver_ErrorsAreRetriedThenTheChainMovesOn(t *testing.T) {
	w := newWorld(t, window, true)
	w.people.wrote("chat", w.now.Add(-time.Hour))
	w.people.wrote("mail", w.now.Add(-time.Hour))
	w.chat.set("", errors.New("connection reset"))

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryPending, d.State)
	for i := 0; i < 2; i++ {
		w.now = w.now.Add(time.Minute)
		w.policy.Sweep(context.Background())
	}
	got := w.only()
	assert.Equal(t, models.DeliveryMoved, got.State)
	assert.Equal(t, 3, got.Attempts)
	assert.Len(t, w.mail.messages(), 1)
}

// A rate limit waits for the time the platform gave, on the same route.
func TestDeliver_RateLimitWaits(t *testing.T) {
	w := newWorld(t, window, true)
	w.people.wrote("chat", w.now.Add(-time.Hour))
	w.people.wrote("mail", w.now.Add(-time.Hour))
	w.chat.retryAt = w.now.Add(10 * time.Minute)
	w.chat.set(channel.RateLimited, nil)

	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryPending, d.State)
	require.NotNil(t, d.NextAttemptAt)
	assert.Equal(t, w.now.Add(10*time.Minute), *d.NextAttemptAt)
	assert.Empty(t, w.mail.messages())

	w.chat.set("", nil)
	w.now = w.now.Add(5 * time.Minute)
	w.policy.Sweep(context.Background())
	assert.Empty(t, w.chat.messages(), "not before the time given")
	w.now = w.now.Add(5 * time.Minute)
	w.policy.Sweep(context.Background())
	assert.Len(t, w.chat.messages(), 1)
	assert.Equal(t, models.DeliveryDelivered, w.only().State)
}

// A held message the person never collects expires, and that outcome is recorded.
func TestSweep_HeldMessageExpires(t *testing.T) {
	w := newWorld(t, sandbox, false)
	w.people.wrote("chat", w.now.Add(-30*time.Hour))
	w.deliver(reminder())
	require.Equal(t, models.DeliveryHeld, w.only().State)

	w.now = w.now.Add(proactive.ReminderHold - time.Minute)
	w.policy.Sweep(context.Background())
	assert.Equal(t, models.DeliveryHeld, w.only().State)
	w.now = w.now.Add(time.Minute)
	w.policy.Sweep(context.Background())
	assert.Equal(t, models.DeliveryExpired, w.only().State)

	w.people.wrote("chat", w.now)
	w.policy.Release(context.Background(), "chat", "+100")
	assert.Empty(t, w.chat.messages(), "an expired message is not sent")
}

// A briefing expires at the time its caller gives (the end of its day).
func TestDeliver_ExpiresAtIsKept(t *testing.T) {
	w := newWorld(t, sandbox, false)
	w.people.wrote("chat", w.now.Add(-30*time.Hour))
	end := w.now.Add(15 * time.Hour)
	d := w.deliver(proactive.Request{Category: proactive.Briefing, Text: "b", ExpiresAt: end})
	assert.Equal(t, end, d.ExpiresAt)
	assert.Equal(t, w.now.Add(proactive.ReminderHold), w.deliver(reminder()).ExpiresAt)
}

// An account that never wrote is not a route: the assistant cannot start a chat there.
func TestDeliver_AccountThatNeverWroteIsNotUsed(t *testing.T) {
	w := newWorld(t, window, true)
	w.people.wrote("mail", w.now.Add(-time.Hour)) // chat never wrote
	d := w.deliver(reminder())
	assert.Equal(t, models.DeliveryDelivered, d.State, "the account they wrote from is the primary one")
	assert.Equal(t, "mail", d.Channel)
	assert.Empty(t, w.chat.messages())
}

func TestDeliver_UnknownPersonRecordsNothing(t *testing.T) {
	w := newWorld(t, window, false)
	_, err := w.policy.Deliver(context.Background(), proactive.Request{Key: "nobody", Category: proactive.Reminder, Text: "x"})
	assert.ErrorIs(t, err, proactive.ErrNoPerson)
	assert.Empty(t, w.store.All())
}

// A row a crash left mid-send is retried after a few minutes.
func TestSweep_RetriesARowLeftSending(t *testing.T) {
	w := newWorld(t, window, false)
	w.now = time.Now() // the store stamps a claim with the real clock
	w.people.wrote("chat", w.now.Add(-time.Hour))
	stuck := &models.ProactiveDelivery{PersonID: personID, Category: proactive.Reminder, Text: "x", State: models.DeliverySending,
		TemplateValues: "null", DueAt: w.now, ExpiresAt: w.now.Add(time.Hour), CreatedAt: w.now, UpdatedAt: w.now.Add(-10 * time.Minute)}
	require.NoError(t, w.store.CreateDelivery(context.Background(), stuck))
	w.policy.Sweep(context.Background()) // made pending, then sent
	w.policy.Sweep(context.Background())
	assert.Equal(t, models.DeliveryDelivered, w.only().State)
	assert.Len(t, w.chat.messages(), 1)
}

// lateRecorder collects what the policy reports through WithLateSend.
type lateRecorder struct {
	mu   sync.Mutex
	rows []models.ProactiveDelivery
}

func (l *lateRecorder) record(_ context.Context, d models.ProactiveDelivery) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, d)
}

func (l *lateRecorder) states() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, d := range l.rows {
		out = append(out, d.State)
	}
	return out
}

func (w *world) withLateSend(l *lateRecorder) {
	reg := channel.NewRegistry()
	require.NoError(w.t, reg.Register(w.chat))
	require.NoError(w.t, reg.Register(w.mail))
	w.policy = proactive.New(w.store, w.people, reg, proactive.WithClock(func() time.Time { return w.now }), proactive.WithLateSend(l.record))
}

// A message that goes out after its first attempt is reported, so the engine can put it in the
// person's chat history; one sent at once is the caller's to record and is not.
func TestLateSend_ReportsEveryMessageSentAfterItsFirstAttempt(t *testing.T) {
	// Sent at once: not reported.
	w := newWorld(t, sender, false)
	late := &lateRecorder{}
	w.withLateSend(late)
	w.people.wrote("chat", w.now.Add(-time.Hour))
	w.deliver(reminder())
	assert.Empty(t, late.states())

	// Re-sent as the template after a later refusal: reported.
	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: w.chat.messages()[0].Ref, Reason: channel.WindowClosed}))
	assert.Equal(t, []string{models.DeliveryDelivered}, late.states())

	// Held, then released when the person writes: reported.
	w = newWorld(t, sandbox, false)
	late = &lateRecorder{}
	w.withLateSend(late)
	w.people.wrote("chat", w.now.Add(-30*time.Hour))
	w.deliver(reminder())
	assert.Empty(t, late.states())
	w.people.wrote("chat", w.now)
	w.policy.Release(context.Background(), "chat", "+100")
	assert.Equal(t, []string{models.DeliveryLate}, late.states())

	// Retried by the minute tick after an error: reported.
	w = newWorld(t, window, false)
	late = &lateRecorder{}
	w.withLateSend(late)
	w.people.wrote("chat", w.now.Add(-time.Hour))
	w.chat.set("", errors.New("connection reset"))
	w.deliver(reminder())
	w.chat.set("", nil)
	w.now = w.now.Add(time.Minute)
	w.policy.Sweep(context.Background())
	assert.Equal(t, []string{models.DeliveryDelivered}, late.states())

	// A briefing's template is only a notice: reported once the briefing itself goes out.
	w = newWorld(t, sender, false)
	late = &lateRecorder{}
	w.withLateSend(late)
	w.people.wrote("chat", w.now.Add(-2*24*time.Hour))
	w.deliver(proactive.Request{Category: proactive.Briefing, Text: "b", TemplateKey: channel.TemplateBriefing})
	require.NoError(t, w.policy.Report(context.Background(), "chat", channel.Report{Ref: "nothing", Reason: channel.WindowClosed}))
	assert.Empty(t, late.states())
	w.policy.Release(context.Background(), "chat", "+100")
	assert.Equal(t, []string{models.DeliveryDelivered}, late.states())
}
