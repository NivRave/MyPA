package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/nivik/mypa/internal/proactive/proactivetest"
	"github.com/nivik/mypa/internal/quiet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openChat is an always-open channel that records what it sends.
type openChat struct {
	mu   sync.Mutex
	sent []string
}

func (c *openChat) Name() string { return "chat" }
func (c *openChat) Capabilities() channel.Capabilities {
	return channel.Capabilities{MaxTextLen: 4000, Markup: channel.PlainText, Proactive: "always"}
}
func (c *openChat) Ingress() channel.Ingress {
	return channel.Ingress{Outward: &channel.Outward{Run: func(ctx context.Context, _ channel.Sink) error { <-ctx.Done(); return ctx.Err() }}}
}
func (c *openChat) Fetch(context.Context, channel.AttachmentRef) (io.ReadCloser, error) {
	return nil, errors.New("no files")
}
func (c *openChat) Send(_ context.Context, _ channel.Address, m channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m.Text)
	return channel.Receipt{Accepted: true, Refs: []string{"r"}}, nil
}
func (c *openChat) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

// omerOnly is the delivery side of one person, Omer (person key "omer"), on the open chat.
type omerOnly struct{}

const omerPerson = "22222222-2222-2222-2222-222222222222"

func (omerOnly) PersonOfKey(_ context.Context, key string) (string, error) {
	if key == "omer" {
		return omerPerson, nil
	}
	return "", nil
}
func (omerOnly) PersonOfAccount(context.Context, string, string) (string, error) {
	return omerPerson, nil
}
func (omerOnly) PersonByID(_ context.Context, id string) (*models.Person, error) {
	return &models.Person{ID: id}, nil
}
func (omerOnly) AccountsOfPerson(context.Context, string) ([]models.ChannelAccount, error) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	return []models.ChannelAccount{{ID: "a", PersonID: omerPerson, Channel: "chat", ExternalID: "omer", LastInboundAt: &at}}, nil
}

// household is the notifier's data: Dana and Omer, the family topic shared by default.
type household struct {
	*identitytest.Store
	users []models.User
}

func (h household) GetSharedSpace(identity.Scope, string) (*models.SharedSpace, error) {
	return nil, nil
}
func (h household) UsersInHousehold(context.Context, identity.Scope) ([]models.User, error) {
	return h.users, nil
}

// BL-49: a partner notification and an event rule's notify: reaching Omer during his quiet hours
// are held in their delivery rows and arrive when the hours end; an urgent one is not held. The
// Google sign-in confirmation, which answers what the person just did, is never held.
func TestPartnerAndRuleNotifications_WaitForTheRecipientsQuietHours(t *testing.T) {
	now := time.Date(2026, 11, 20, 21, 30, 0, 0, time.UTC) // 23:30 in Jerusalem
	clock := func() time.Time { return now }
	chat := &openChat{}
	reg := channel.NewRegistry()
	require.NoError(t, reg.Register(chat))
	reg.Seal()
	store := proactivetest.NewStore()
	omerClock := quiet.NewClock(quiet.Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, "")
	policy := proactive.New(store, omerOnly{}, reg, proactive.WithClock(clock),
		proactive.WithQuietHours(func(context.Context, string) quiet.Clock { return omerClock }))

	base := channelNotifier{channels: reg, delivery: policy}
	partner := base.respectingQuietHours()

	ids := identitytest.NewStore()
	h := ids.Household("bl49")
	notifier := shared.NewNotifier(household{Store: ids, users: []models.User{
		{ID: "u-dana", PlatformID: "dana", Name: "Dana", HouseholdID: h.ID},
		{ID: "u-omer", PlatformID: "omer", Name: "Omer", HouseholdID: h.ID},
	}}, partner.SendMessage)
	runner := automation.NewRunner(nil, nil, automation.WithNotificationSender(partner.SendMessage))
	ctx := context.Background()

	// A partner notification: Dana logs a family entry.
	require.NoError(t, notifier.HandleMutationEvent(ctx, events.MutationEvent{
		Topic: "family", Action: "create", EntityType: "FamilyNote", ActorUserID: "dana", HouseholdID: h.ID, Summary: "school trip form",
	}))
	// An event rule of Omer's with a notify: action.
	rule := models.AutomationRule{ID: 7, UserID: "omer", HouseholdID: h.ID, TriggerType: "event_mutation", ActionInstruction: "notify: Dana spent a lot", IsActive: true}
	require.NoError(t, runner.ExecuteAction(ctx, rule, events.MutationEvent{Topic: "finance", Action: "create", HouseholdID: h.ID}))
	// The same rule marked urgent.
	urgent := rule
	urgent.ID, urgent.ConditionJSON, urgent.ActionInstruction = 8, `{"urgent": true}`, "notify: the alarm went off"
	require.NoError(t, runner.ExecuteAction(ctx, urgent, events.MutationEvent{Topic: "house", Action: "create", HouseholdID: h.ID}))
	// A partner notification about an urgent reminder.
	require.NoError(t, notifier.HandleMutationEvent(ctx, events.MutationEvent{
		Topic: "family", Action: "create", EntityType: "SharedTaskReminder", ActorUserID: "dana", HouseholdID: h.ID,
		Summary: "give Noa her medicine", AfterJSON: `{"title":"give Noa her medicine","urgent":true}`,
	}))
	// The Google sign-in confirmation goes through the plain notifier.
	require.NoError(t, base.SendMessage(ctx, "omer", "✅ Google connected"))

	assert.ElementsMatch(t, []string{
		"the alarm went off",
		"📢 [Shared • Family] Dana created task: give Noa her medicine",
		"✅ Google connected",
	}, chat.texts(), "only the urgent ones and the sign-in confirmation go out during quiet hours")

	now = time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC) // 07:00 in Jerusalem
	policy.Sweep(ctx)
	assert.Contains(t, chat.texts(), "📢 [Shared • Family] Dana logged: school trip form", "held partner notification arrives when the quiet hours end")
	assert.Contains(t, chat.texts(), "Dana spent a lot", "held rule notification arrives when the quiet hours end")
	assert.Len(t, chat.texts(), 5)
}
