package automation

import (
	"context"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-67: a household rule does not act on another member's unshared row. Omer's rule does not
// fire on Dana's private transaction; it fires on her shared one and on his own private one. A
// delete carries only the before-image, which counts the same way.
func TestRunner_ARuleSkipsAnotherMembersUnsharedRow(t *testing.T) {
	repo := newFakeAutomationRepo()
	runner := NewRunner(repo, nil)
	var mu sync.Mutex
	var fired []string
	runner.actionExecutor = func(_ context.Context, _ models.AutomationRule, ev events.MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, ev.EntityID)
		return nil
	}

	store := identitytest.NewStore()
	omer := store.ScopeIn(t, "bl67", "omer", "family")
	require.NoError(t, repo.CreateAutomationRule(omer, &models.AutomationRule{
		UserID: "omer", Name: "Big spend", TriggerType: "event_mutation", TriggerTopic: "finance",
		ConditionJSON: `{"min_amount": 500}`, ActionInstruction: "webhook: https://example.com/hook", IsActive: true,
	}))
	require.NoError(t, repo.CreateAutomationRule(omer, &models.AutomationRule{
		UserID: "omer", Name: "Any delete", TriggerType: "event_mutation", TriggerTopic: "finance",
		ConditionJSON: `{"action": "delete"}`, ActionInstruction: "webhook: https://example.com/hook", IsActive: true,
	}))

	ctx := context.Background()
	tx := func(id, actor, after string) events.MutationEvent {
		return events.MutationEvent{Topic: "finance", Action: "create", EntityType: "FinancialTransaction", EntityID: id,
			ActorUserID: actor, HouseholdID: omer.HouseholdID(), AfterJSON: after}
	}
	for _, ev := range []events.MutationEvent{
		tx("dana-private", "dana", `{"amount":900,"merchant":"Clinic","is_shared":false}`),
		tx("dana-shared", "dana", `{"amount":900,"merchant":"Market","is_shared":true}`),
		tx("omer-private", "omer", `{"amount":900,"merchant":"Bike shop","is_shared":false}`),
		{Topic: "finance", Action: "delete", EntityType: "FinancialTransaction", EntityID: "dana-private-delete",
			ActorUserID: "dana", HouseholdID: omer.HouseholdID(), BeforeJSON: `{"amount":900,"is_shared":false}`},
	} {
		require.NoError(t, runner.HandleMutationEvent(ctx, ev))
	}

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"dana-shared", "omer-private"}, fired)
}

// The finance tools name the actor by users.id (people.id), the rule tools by the platform ID:
// Omer's own private transaction logged in chat is still his, through the rule's person_id.
func TestRunner_AnActorNamedByPersonIDIsTheRulesPerson(t *testing.T) {
	repo := newFakeAutomationRepo()
	runner := NewRunner(repo, nil)
	var fired []string
	runner.actionExecutor = func(_ context.Context, _ models.AutomationRule, ev events.MutationEvent) error {
		fired = append(fired, ev.EntityID)
		return nil
	}
	store := identitytest.NewStore()
	omer := store.ScopeIn(t, "bl67-person", "omer-platform", "family")
	omerPerson := "33333333-3333-3333-3333-333333333333"
	require.NoError(t, repo.CreateAutomationRule(omer, &models.AutomationRule{
		UserID: "omer-platform", PersonID: &omerPerson, Name: "Big spend", TriggerType: "event_mutation", TriggerTopic: "finance",
		ConditionJSON: `{"min_amount": 500}`, ActionInstruction: "notify: big spend", IsActive: true,
	}))
	for _, ev := range []events.MutationEvent{
		{Topic: "finance", Action: "create", EntityID: "omer-private-by-person-id", ActorUserID: omerPerson,
			HouseholdID: omer.HouseholdID(), AfterJSON: `{"amount":900,"is_shared":false}`},
		{Topic: "finance", Action: "create", EntityID: "dana-private-by-person-id", ActorUserID: "44444444-4444-4444-4444-444444444444",
			HouseholdID: omer.HouseholdID(), AfterJSON: `{"amount":900,"is_shared":false}`},
	} {
		require.NoError(t, runner.HandleMutationEvent(context.Background(), ev))
	}
	assert.Equal(t, []string{"omer-private-by-person-id"}, fired)
}
