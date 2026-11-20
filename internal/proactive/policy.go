// Package proactive is the delivery policy for messages the assistant starts itself: reminders,
// briefings and notifications (ROADMAP V4-33, ADR 0003 Decision 3).
//
// Every such message becomes a row (models.ProactiveDelivery) before it is sent. The route is
// chosen from each channel's declared rule (channel.ProactiveRule) and when the person last wrote
// on each of their accounts, in the order the ADR fixes:
//
//  1. the preferred account, if its channel accepts free text now;
//  2. an approved template on it, if the channel has one for the message;
//  3. another of the person's accounts whose channel accepts free text now;
//  4. otherwise hold the message until the person next writes, then send it there with a note
//     that it is late.
//
// The prediction can be wrong (the platform's clock is the truth), so a refusal the platform
// reports later (channel.Report) reopens the row and the chain runs again without the refused
// route. Each message ends as delivered, delivered late, moved to another channel, or expired.
// The package names no platform.
package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
)

// Categories of proactive message.
const (
	Reminder     = "reminder"
	Briefing     = "briefing"
	Notification = "notification"
)

// How long an undeliverable message is held (ADR 0003 §11): a reminder 7 days, a briefing until
// the end of its day (the caller sets ExpiresAt). A notification is held as long as a reminder.
const (
	ReminderHold     = 7 * 24 * time.Hour
	NotificationHold = 7 * 24 * time.Hour
)

const (
	// maxAttempts is how often a route that fails with an error (not a refusal) is tried before
	// the chain moves on from it.
	maxAttempts = 3
	retryDelay  = time.Minute
	// stuckAfter is when a row left in DeliverySending (a crash mid-send) is retried.
	stuckAfter = 5 * time.Minute
	// keepFor is how long a row that ended is kept, then purged.
	keepFor = 30 * 24 * time.Hour
)

// Ended lists the states a message ends in (a later refusal can still reopen all but expired).
var Ended = []string{models.DeliveryDelivered, models.DeliveryLate, models.DeliveryMoved, models.DeliveryExpired}

// ErrNoPerson is returned for a message to a key that names no person: there is no account to
// choose a route from. The caller keeps its own record (a reminder stays due).
var ErrNoPerson = errors.New("proactive: the recipient is not a known person")

// Store keeps the delivery rows. db.Client implements it; proactivetest has one in memory.
type Store interface {
	CreateDelivery(ctx context.Context, d *models.ProactiveDelivery) error
	SaveDelivery(ctx context.Context, d *models.ProactiveDelivery) error
	// ClaimDelivery moves the row to DeliverySending if it is in one of the states, and reports
	// whether it did: only one attempt sends a row at a time.
	ClaimDelivery(ctx context.Context, id string, from []string) (bool, error)
	// HeldDeliveries lists the person's held rows, oldest first.
	HeldDeliveries(ctx context.Context, personID string) ([]models.ProactiveDelivery, error)
	// DueDeliveries lists the pending rows whose next attempt is due.
	DueDeliveries(ctx context.Context, now time.Time) ([]models.ProactiveDelivery, error)
	// DeliveryByRef finds the row whose last send on the channel carried the platform ID ref.
	DeliveryByRef(ctx context.Context, channelName, ref string) (*models.ProactiveDelivery, error)
	// ExpireDeliveries ends every held or pending row whose ExpiresAt has passed, and returns them.
	ExpireDeliveries(ctx context.Context, now time.Time) ([]models.ProactiveDelivery, error)
	// UnstickDeliveries makes rows left in DeliverySending since before the time pending again.
	UnstickDeliveries(ctx context.Context, before time.Time) (int64, error)
	// PurgeDeliveries deletes rows that ended before the time.
	PurgeDeliveries(ctx context.Context, before time.Time) (int64, error)
}

// People finds a person and their channel accounts (V4-32). db.Client implements it.
type People interface {
	PersonOfKey(ctx context.Context, key string) (string, error)
	PersonOfAccount(ctx context.Context, channelName, externalID string) (string, error)
	PersonByID(ctx context.Context, id string) (*models.Person, error)
	AccountsOfPerson(ctx context.Context, personID string) ([]models.ChannelAccount, error)
}

// Channels sends through the registered adapters. *channel.Registry implements it.
type Channels interface {
	Get(name string) (channel.Adapter, bool)
	Send(ctx context.Context, to channel.Address, m channel.Outbound) (channel.Receipt, error)
	JoinSteps() []channel.JoinStep
}

// Request is one message to deliver.
type Request struct {
	// Key names the person: a person key as reminders carry it (a platform ID or people.id).
	Key      string
	Category string
	// Text is the full message, in the Markdown subset the adapters convert.
	Text string
	// TemplateKey and TemplateValues are the template it may go out as outside a window, if the
	// channel has it (channel.TemplateReminder, channel.TemplateBriefing); empty for none.
	TemplateKey    string
	TemplateValues []string
	// DueAt is when the message was due; zero means now.
	DueAt time.Time
	// ExpiresAt is when a held message is given up; zero means by its category.
	ExpiresAt time.Time
}

// Policy decides and records how each proactive message is delivered.
type Policy struct {
	store    Store
	people   People
	channels Channels
	now      func() time.Time
	zone     func(ctx context.Context, personID string) *time.Location
}

// Option configures a Policy.
type Option func(*Policy)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option { return func(p *Policy) { p.now = now } }

// WithZone gives the person's time zone, for the note on a late message. Without it, UTC.
func WithZone(zone func(ctx context.Context, personID string) *time.Location) Option {
	return func(p *Policy) { p.zone = zone }
}

// New returns a policy over the store, the people and the channels.
func New(store Store, people People, channels Channels, opts ...Option) *Policy {
	p := &Policy{store: store, people: people, channels: channels, now: time.Now}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Deliver records the message and sends it by the first open route, or holds it. It returns the
// row as it ended this attempt. An error means nothing was recorded.
func (p *Policy) Deliver(ctx context.Context, req Request) (*models.ProactiveDelivery, error) {
	personID, err := p.people.PersonOfKey(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("proactive: find the recipient: %w", err)
	}
	if personID == "" {
		return nil, ErrNoPerson
	}
	now := p.now()
	values, err := json.Marshal(req.TemplateValues)
	if err != nil {
		return nil, err
	}
	d := &models.ProactiveDelivery{
		PersonID:       personID,
		Category:       req.Category,
		Text:           req.Text,
		TemplateKey:    req.TemplateKey,
		TemplateValues: string(values),
		State:          models.DeliverySending,
		DueAt:          req.DueAt,
		ExpiresAt:      req.ExpiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if d.DueAt.IsZero() {
		d.DueAt = now
	}
	if d.ExpiresAt.IsZero() {
		d.ExpiresAt = now.Add(holdFor(req.Category))
	}
	if err := p.store.CreateDelivery(ctx, d); err != nil {
		return nil, fmt.Errorf("proactive: record the message: %w", err)
	}
	p.attempt(ctx, d)
	return d, nil
}

func holdFor(category string) time.Duration {
	if category == Reminder {
		return ReminderHold
	}
	return NotificationHold
}

// account is one of the person's accounts as a route sees it.
type account struct {
	models.ChannelAccount
	rule     channel.ProactiveRule
	openness channel.Openness
}

func (a account) address() channel.Address {
	return channel.Address{Channel: a.Channel, Person: a.ExternalID}
}

func accountKey(channelName, externalID string) string { return channelName + ":" + externalID }

func (a account) key() string { return accountKey(a.Channel, a.ExternalID) }

// route is one way to send the message.
type route struct {
	kind string // models.RouteDirect, RouteTemplate or RouteOther
	to   account
	// note is appended to the text: why it came on another channel and how to be reached there
	// again.
	note string
}

// skipKey is the Skip entry that rules this route out.
func (r route) skipKey() string {
	if r.kind == models.RouteTemplate {
		return r.to.key() + "#template"
	}
	return r.to.key()
}

// accounts returns the person's usable accounts, the primary first: their preferred account,
// else the one they last wrote from, then the rest by when they last wrote. An account on a
// channel this install does not run, or that never wrote, is left out.
func (p *Policy) accounts(ctx context.Context, personID string, now time.Time) ([]account, error) {
	person, err := p.people.PersonByID(ctx, personID)
	if err != nil {
		return nil, err
	}
	all, err := p.people.AccountsOfPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	var out []account
	for _, a := range all {
		adapter, ok := p.channels.Get(a.Channel)
		if !ok || a.LastInboundAt == nil {
			continue
		}
		rule := adapter.Capabilities().Rule
		out = append(out, account{ChannelAccount: a, rule: rule, openness: rule.At(*a.LastInboundAt, now)})
	}
	preferred := ""
	if person != nil && person.PreferredAccountID != nil {
		preferred = *person.PreferredAccountID
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].ID == preferred) != (out[j].ID == preferred) {
			return out[i].ID == preferred
		}
		return out[i].LastInboundAt.After(*out[j].LastInboundAt)
	})
	return out, nil
}

// routes lists, in order, the routes open for the message now.
func (p *Policy) routes(d *models.ProactiveDelivery, accts []account) []route {
	if len(accts) == 0 {
		return nil
	}
	skip := skipSet(d.Skip)
	primary := accts[0]
	var out []route
	if primary.openness == channel.Open && !skip[primary.key()] {
		out = append(out, route{kind: models.RouteDirect, to: primary})
	}
	if primary.openness != channel.Unreachable && d.TemplateKey != "" && primary.rule.HasTemplate(d.TemplateKey) {
		if r := (route{kind: models.RouteTemplate, to: primary}); !skip[r.skipKey()] {
			out = append(out, r)
		}
	}
	note := ""
	if primary.openness == channel.Unreachable || skip[primary.key()+"!"] {
		note = p.rejoinNote(primary.Channel)
	}
	for _, a := range accts[1:] {
		if a.openness == channel.Open && !skip[a.key()] {
			out = append(out, route{kind: models.RouteOther, to: a, note: note})
		}
	}
	return out
}

// rejoinNote tells the person, on another channel, that the first could not reach them and what
// to do there, when the channel has a step before the first message (a sandbox's join message).
func (p *Policy) rejoinNote(channelName string) string {
	for _, s := range p.channels.JoinSteps() {
		if s.Channel == channelName && s.Step != "" {
			return fmt.Sprintf("\n\nℹ️ I could not reach you on %s, so this came here. %s", channelName, s.Step)
		}
	}
	return ""
}

// attempt runs the chain for a row in DeliverySending and saves where it ended.
func (p *Policy) attempt(ctx context.Context, d *models.ProactiveDelivery) {
	now := p.now()
	accts, err := p.accounts(ctx, d.PersonID, now)
	if err != nil {
		p.retryLater(ctx, d, now, "accounts: "+err.Error())
		return
	}
	for _, r := range p.routes(d, accts) {
		rcpt, err := p.send(ctx, d, r)
		var refused *channel.RefusedError
		switch {
		case err == nil:
			p.sent(ctx, d, r, rcpt, now)
			return
		case errors.As(err, &refused):
			d.Reason = string(refused.Reason)
			if refused.Reason == channel.RateLimited {
				next := rcpt.RetryAt
				if next.IsZero() {
					next = now.Add(retryDelay)
				}
				p.pending(ctx, d, next)
				return
			}
			d.Skip = addSkip(d.Skip, skipKeysFor(r, refused.Reason)...)
		default:
			d.Attempts++
			d.Reason = "error"
			slog.Warn("proactive message not sent", "delivery_id", d.ID, "channel", r.to.Channel, "route", r.kind, "attempt", d.Attempts, "error", err)
			if d.Attempts < maxAttempts {
				p.pending(ctx, d, now.Add(retryDelay))
				return
			}
			d.Skip = addSkip(d.Skip, r.skipKey())
		}
	}
	d.State, d.Route, d.Refs = models.DeliveryHeld, "", ""
	p.save(ctx, d)
	slog.Info("proactive message held until the person writes", "delivery_id", d.ID, "category", d.Category, "person_id", d.PersonID, "reason", d.Reason)
}

// skipKeysFor is what a refusal rules out: the route, and for an unreachable account everything
// on it.
func skipKeysFor(r route, reason channel.RefusalReason) []string {
	if reason == channel.NotReachable {
		return []string{r.to.key(), r.to.key() + "#template", r.to.key() + "!"}
	}
	return []string{r.skipKey()}
}

// send sends the message by one route.
func (p *Policy) send(ctx context.Context, d *models.ProactiveDelivery, r route) (channel.Receipt, error) {
	out := channel.Outbound{Text: d.Text + r.note, Proactive: &channel.Proactive{Category: d.Category}}
	if r.kind == models.RouteTemplate {
		var values []string
		if d.TemplateValues != "" {
			if err := json.Unmarshal([]byte(d.TemplateValues), &values); err != nil {
				return channel.Receipt{}, fmt.Errorf("template values: %w", err)
			}
		}
		out.Proactive.TemplateKey, out.Proactive.TemplateValues = d.TemplateKey, values
	}
	return p.channels.Send(ctx, r.to.address(), out)
}

// sent records an accepted send. A briefing's template only says the briefing is waiting, so the
// row stays held for the full text, which goes out when the person replies.
func (p *Policy) sent(ctx context.Context, d *models.ProactiveDelivery, r route, rcpt channel.Receipt, now time.Time) {
	d.Route, d.Channel, d.ExternalID = r.kind, r.to.Channel, r.to.ExternalID
	d.Refs = strings.Join(rcpt.Refs, ",")
	d.NextAttemptAt = nil
	switch {
	case r.kind == models.RouteTemplate && d.Category == Briefing:
		d.State = models.DeliveryHeld
	case r.kind == models.RouteOther:
		d.State, d.DeliveredAt = models.DeliveryMoved, &now
	default:
		d.State, d.DeliveredAt = models.DeliveryDelivered, &now
	}
	p.save(ctx, d)
	slog.Info("proactive message sent", "delivery_id", d.ID, "category", d.Category, "person_id", d.PersonID, "channel", d.Channel, "route", d.Route, "state", d.State)
}

func (p *Policy) pending(ctx context.Context, d *models.ProactiveDelivery, next time.Time) {
	d.State, d.NextAttemptAt = models.DeliveryPending, &next
	p.save(ctx, d)
}

func (p *Policy) retryLater(ctx context.Context, d *models.ProactiveDelivery, now time.Time, why string) {
	slog.Warn("proactive message not attempted", "delivery_id", d.ID, "error", why)
	d.Reason = "error"
	p.pending(ctx, d, now.Add(retryDelay))
}

func (p *Policy) save(ctx context.Context, d *models.ProactiveDelivery) {
	d.UpdatedAt = p.now()
	if err := p.store.SaveDelivery(ctx, d); err != nil {
		// The row stays in its last saved state; a row left sending is retried by Sweep.
		slog.Error("proactive delivery state not saved", "delivery_id", d.ID, "state", d.State, "error", err)
	}
}

// Release sends the person's held messages to the account they just wrote from, which is open
// because they wrote. A late one says so; a briefing whose template already said it was waiting
// is simply delivered. The engine calls it for every real inbound message.
func (p *Policy) Release(ctx context.Context, channelName, externalID string) {
	personID, err := p.people.PersonOfAccount(ctx, channelName, externalID)
	if err != nil || personID == "" {
		return
	}
	held, err := p.store.HeldDeliveries(ctx, personID)
	if err != nil {
		slog.Warn("held proactive messages not read", "person_id", personID, "error", err)
		return
	}
	for i := range held {
		d := &held[i]
		ok, err := p.store.ClaimDelivery(ctx, d.ID, []string{models.DeliveryHeld})
		if err != nil || !ok {
			continue
		}
		noticed := d.Route == models.RouteTemplate
		text := d.Text
		if !noticed {
			text = p.lateNote(ctx, d) + text
		}
		to := channel.Address{Channel: channelName, Person: externalID}
		rcpt, err := p.channels.Send(ctx, to, channel.Outbound{Text: text, Proactive: &channel.Proactive{Category: d.Category}})
		if err != nil {
			d.State = models.DeliveryHeld // still held: the next message tries again
			d.Reason = "error"
			p.save(ctx, d)
			slog.Warn("held proactive message not sent", "delivery_id", d.ID, "error", err)
			continue
		}
		now := p.now()
		d.Route, d.Channel, d.ExternalID = models.RouteOnReply, channelName, externalID
		d.Refs, d.DeliveredAt = strings.Join(rcpt.Refs, ","), &now
		d.State = models.DeliveryLate
		if noticed {
			d.State = models.DeliveryDelivered
		}
		p.save(ctx, d)
		slog.Info("held proactive message sent", "delivery_id", d.ID, "category", d.Category, "person_id", d.PersonID, "channel", channelName, "state", d.State)
	}
}

// lateNote opens a message that was held: when it was due, on the person's clock.
func (p *Policy) lateNote(ctx context.Context, d *models.ProactiveDelivery) string {
	loc := time.UTC
	if p.zone != nil {
		if l := p.zone(ctx, d.PersonID); l != nil {
			loc = l
		}
	}
	return fmt.Sprintf("⏰ This was due %s, when I could not reach you.\n\n", d.DueAt.In(loc).Format("Mon 2 Jan, 15:04"))
}

// Report handles a platform's later refusal of a message it had accepted. The row is reopened
// and the chain runs again without the refused route. A report on a message that was not
// proactive, or that was already handled, matches nothing and is ignored.
func (p *Policy) Report(ctx context.Context, channelName string, r channel.Report) error {
	if r.Ref == "" {
		return nil
	}
	d, err := p.store.DeliveryByRef(ctx, channelName, r.Ref)
	if err != nil || d == nil {
		return err
	}
	ok, err := p.store.ClaimDelivery(ctx, d.ID, []string{models.DeliveryDelivered, models.DeliveryMoved, models.DeliveryLate, models.DeliveryHeld})
	if err != nil || !ok {
		return err
	}
	slog.Info("proactive message refused after it was sent", "delivery_id", d.ID, "category", d.Category, "channel", channelName, "route", d.Route, "reason", r.Reason)
	d.State, d.Reason, d.Refs, d.DeliveredAt = models.DeliverySending, string(r.Reason), "", nil
	if r.Reason == channel.RateLimited {
		p.pending(ctx, d, p.now().Add(retryDelay))
		return nil
	}
	refused := route{kind: d.Route, to: account{ChannelAccount: models.ChannelAccount{Channel: d.Channel, ExternalID: d.ExternalID}}}
	if refused.kind == models.RouteOnReply || refused.kind == models.RouteOther {
		refused.kind = models.RouteDirect // free text on that account
	}
	d.Skip = addSkip(d.Skip, skipKeysFor(refused, r.Reason)...)
	p.attempt(ctx, d)
	return nil
}

// Sweep is the minute tick: a row a crash left sending is retried, held rows past their time
// expire, pending rows whose retry is due are sent, and rows that ended a month ago are purged.
func (p *Policy) Sweep(ctx context.Context) {
	now := p.now()
	if n, err := p.store.UnstickDeliveries(ctx, now.Add(-stuckAfter)); err != nil {
		slog.Warn("stuck proactive messages not reset", "error", err)
	} else if n > 0 {
		slog.Warn("proactive messages left mid-send are retried", "count", n)
	}
	expired, err := p.store.ExpireDeliveries(ctx, now)
	if err != nil {
		slog.Warn("held proactive messages not expired", "error", err)
	}
	for _, d := range expired {
		slog.Info("proactive message expired undelivered", "delivery_id", d.ID, "category", d.Category, "person_id", d.PersonID, "reason", d.Reason)
	}
	due, err := p.store.DueDeliveries(ctx, now)
	if err != nil {
		slog.Warn("pending proactive messages not read", "error", err)
	}
	for i := range due {
		d := &due[i]
		if ok, err := p.store.ClaimDelivery(ctx, d.ID, []string{models.DeliveryPending}); err == nil && ok {
			d.State = models.DeliverySending
			p.attempt(ctx, d)
		}
	}
	if _, err := p.store.PurgeDeliveries(ctx, now.Add(-keepFor)); err != nil {
		slog.Warn("old proactive messages not purged", "error", err)
	}
}

func skipSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, k := range strings.Split(s, ",") {
		if k != "" {
			out[k] = true
		}
	}
	return out
}

func addSkip(s string, keys ...string) string {
	set := skipSet(s)
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
	for _, k := range keys {
		if !set[k] {
			set[k] = true
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, ",")
}
