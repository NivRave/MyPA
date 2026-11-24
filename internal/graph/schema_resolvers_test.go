package graph

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type eventTracker struct {
	mu     sync.Mutex
	events []events.MutationEvent
}

func (et *eventTracker) record(ctx context.Context, ev events.MutationEvent) error {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.events = append(et.events, ev)
	return nil
}

func (et *eventTracker) getEvents() []events.MutationEvent {
	et.mu.Lock()
	defer et.mu.Unlock()
	out := make([]events.MutationEvent, len(et.events))
	copy(out, et.events)
	return out
}

func setupTestResolver(t *testing.T) (*Resolver, *eventTracker, func()) {
	t.Helper()

	client, cleanup := testutil.NewTestDB(t)

	bus := events.NewEventBus()
	tracker := &eventTracker{}
	bus.Subscribe(tracker.record)

	resolver := NewResolver(ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	return resolver, tracker, cleanup
}

func TestSchemaResolvers_BabyLifecycle(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	famA := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	households(t, resolver.DB, famA)

	ctxA := signedIn(t, resolver, famA, userA, "member")

	// 1. UpsertBabyProfile
	dob := time.Now().Add(-60 * 24 * time.Hour)
	weight := 4500
	height := 57.0
	profile, err := resolver.Mutation().UpsertBabyProfile(ctxA, "Noah", dob, "male", &weight, &height, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "Noah", profile.Name)
	assert.Equal(t, famA, profile.FamilyGroup)

	// 2. Query BabyProfiles
	profiles, err := resolver.Query().BabyProfiles(ctxA)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, profile.ID, profiles[0].ID)

	// 3. LogBabyCare
	volume := 160.0
	unit := "ml"
	details := "Evening bottle feed"
	careLog, err := resolver.Mutation().LogBabyCare(ctxA, profile.ID, "feeding", &volume, &unit, &details, nil)
	require.NoError(t, err)
	require.NotNil(t, careLog)
	assert.Equal(t, "feeding", careLog.LogType)

	// 4. Query BabyCareLogs
	careLogs, err := resolver.Query().BabyCareLogs(ctxA, profile.ID, nil, nil)
	require.NoError(t, err)
	require.Len(t, careLogs, 1)
	assert.Equal(t, careLog.ID, careLogs[0].ID)

	// 5. UpdateBabyInventory
	item, err := resolver.Mutation().UpdateBabyInventory(ctxA, "Diapers Size 2", 120.0, "count", 25.0)
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, "Diapers Size 2", item.ItemName)

	// 6. Query BabyInventory
	inventory, err := resolver.Query().BabyInventory(ctxA)
	require.NoError(t, err)
	require.Len(t, inventory, 1)
	assert.Equal(t, item.ID, inventory[0].ID)

	// 7. Verify MutationEvents emitted
	time.Sleep(50 * time.Millisecond)
	recorded := tracker.getEvents()
	require.True(t, len(recorded) >= 3, "Expected at least 3 mutation events recorded")

	topics := make([]string, 0, len(recorded))
	for _, ev := range recorded {
		topics = append(topics, ev.Topic)
		assert.Equal(t, famA, ev.FamilyGroup)
	}
	assert.Contains(t, topics, "baby")
}

func TestSchemaResolvers_FinanceLifecycle(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-" + uuid.New().String()
	households(t, resolver.DB, fam)
	user := "usr-" + uuid.New().String()

	ctx := signedIn(t, resolver, fam, user, "member")

	// 1. LogFinancialTransaction
	notes := "Weekly grocery shopping"
	isShared := true
	tx, err := resolver.Mutation().LogFinancialTransaction(ctx, "expense", "groceries", "Trader Joe's", 84.50, "USD", &notes, &isShared)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, 84.50, tx.Amount)

	// 2. SetCategoryBudget
	threshold := 80.0
	budget, err := resolver.Mutation().SetCategoryBudget(ctx, "groceries", 600.0, "USD", &threshold)
	require.NoError(t, err)
	require.NotNil(t, budget)
	assert.Equal(t, 600.0, budget.MonthlyLimit)

	// 3. SaveRecurringBill
	bill, err := resolver.Mutation().SaveRecurringBill(ctx, "Broadband Internet", "utilities", 65.0, "USD", "monthly", 20, true)
	require.NoError(t, err)
	require.NotNil(t, bill)
	assert.Equal(t, "Broadband Internet", bill.Name)

	// 4. Query all three
	txs, err := resolver.Query().FinancialTransactions(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, txs, 1)

	budgets, err := resolver.Query().CategoryBudgets(ctx)
	require.NoError(t, err)
	require.Len(t, budgets, 1)

	bills, err := resolver.Query().RecurringBills(ctx)
	require.NoError(t, err)
	require.Len(t, bills, 1)

	// 5. Verify MutationEvents
	time.Sleep(50 * time.Millisecond)
	recorded := tracker.getEvents()
	require.True(t, len(recorded) >= 3)
	for _, ev := range recorded {
		assert.Equal(t, "finance", ev.Topic)
		assert.Equal(t, fam, ev.FamilyGroup)
	}
}

func TestSchemaResolvers_ScheduleLifecycle(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-" + uuid.New().String()
	households(t, resolver.DB, fam)
	user := "usr-" + uuid.New().String()

	ctx := signedIn(t, resolver, fam, user, "member")

	startTime := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	endTime := startTime.Add(2 * time.Hour)
	desc := "Annual dental checkup"
	syncGoogle := true
	isShared := true

	// 1. CreateScheduleEvent
	ev, err := resolver.Mutation().CreateScheduleEvent(ctx, "health", "Dentist Appointment", &desc, startTime, endTime, nil, &syncGoogle, &isShared)
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, "Dentist Appointment", ev.Title)

	// 2. Query ScheduleEvents
	eventsList, err := resolver.Query().ScheduleEvents(ctx, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, eventsList, 1)
	assert.Equal(t, ev.ID, eventsList[0].ID)

	// 3. DeleteScheduleEvent
	deleted, err := resolver.Mutation().DeleteScheduleEvent(ctx, ev.ID)
	require.NoError(t, err)
	assert.True(t, deleted)

	// Query after delete
	eventsAfter, err := resolver.Query().ScheduleEvents(ctx, nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, eventsAfter)

	// 4. Verify MutationEvents
	time.Sleep(50 * time.Millisecond)
	recorded := tracker.getEvents()
	require.True(t, len(recorded) >= 2)
	assert.Equal(t, "schedule", recorded[0].Topic)
	assert.Equal(t, "create", recorded[0].Action)
	assert.Equal(t, "schedule", recorded[1].Topic)
	assert.Equal(t, "delete", recorded[1].Action)
}

func TestSchemaResolvers_HouseLifecycle(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-" + uuid.New().String()
	households(t, resolver.DB, fam)
	user := "usr-" + uuid.New().String()

	ctx := signedIn(t, resolver, fam, user, "member")

	// 1. SaveHouseContract
	exp := time.Now().Add(90 * 24 * time.Hour)
	landlord := "Prime Estates"
	amount := 2400.0
	currency := "USD"
	noticeDays := 60
	contract, err := resolver.Mutation().SaveHouseContract(ctx, "lease", "Apartment 4B", &landlord, &amount, &currency, &exp, &noticeDays, nil)
	require.NoError(t, err)
	require.NotNil(t, contract)

	// 2. SaveHomeMaintenanceItem
	nextDue := time.Now().Add(30 * 24 * time.Hour)
	notes := "Check filter dimensions"
	maint, err := resolver.Mutation().SaveHomeMaintenanceItem(ctx, "HVAC Inspection", "hvac", 180, nextDue, nil, &notes)
	require.NoError(t, err)
	require.NotNil(t, maint)

	// 3. CompleteHomeMaintenance
	completedMaint, err := resolver.Mutation().CompleteHomeMaintenance(ctx, maint.ID)
	require.NoError(t, err)
	require.NotNil(t, completedMaint)
	assert.NotNil(t, completedMaint.LastCompletedAt)

	// 4. SaveVehicleAsset
	insExpiry := time.Now().Add(180 * 24 * time.Hour)
	mileage := 35000
	nextService := 40000
	veh, err := resolver.Mutation().SaveVehicleAsset(ctx, "Mazda CX-5", "KLP-5544", &insExpiry, nil, &mileage, &nextService, nil)
	require.NoError(t, err)
	require.NotNil(t, veh)

	// 5. Query all
	contracts, err := resolver.Query().HouseContracts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, contracts, 1)

	maints, err := resolver.Query().HomeMaintenanceItems(ctx, nil)
	require.NoError(t, err)
	require.Len(t, maints, 1)

	vehs, err := resolver.Query().VehicleAssets(ctx)
	require.NoError(t, err)
	require.Len(t, vehs, 1)

	// 6. Verify MutationEvents
	time.Sleep(50 * time.Millisecond)
	recorded := tracker.getEvents()
	require.True(t, len(recorded) >= 4)
	for _, ev := range recorded {
		assert.Equal(t, "house", ev.Topic)
		assert.Equal(t, fam, ev.FamilyGroup)
	}
}

func TestSchemaResolvers_AutomationLifecycle(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-" + uuid.New().String()
	households(t, resolver.DB, fam)
	user := "usr-" + uuid.New().String()

	ctx := signedIn(t, resolver, fam, user, "member")

	// 1. RegisterExternalAPI
	method := "POST"
	desc := "Local webhook gateway"
	api, err := resolver.Mutation().RegisterExternalAPI(ctx, "LocalGateway", "https://93.184.216.34/webhook", &method, &desc)
	require.NoError(t, err)
	require.NotNil(t, api)
	assert.Equal(t, fam, api.FamilyGroup)

	// 2. SaveAutomationRule
	active := true
	rule, err := resolver.Mutation().SaveAutomationRule(ctx, "EveningNotify", "cron", "shared", `{"cron":"0 20 * * *"}`, "Send evening digest", &active)
	require.NoError(t, err)
	require.NotNil(t, rule)
	assert.Equal(t, fam, rule.FamilyGroup)

	// 3. Query
	apis, err := resolver.Query().ExternalAPIConnectors(ctx)
	require.NoError(t, err)
	require.Len(t, apis, 1)

	rules, err := resolver.Query().AutomationRules(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	// 4. ToggleAutomationRule
	toggled, err := resolver.Mutation().ToggleAutomationRule(ctx, rule.ID, false)
	require.NoError(t, err)
	require.NotNil(t, toggled)
	assert.False(t, toggled.IsActive)

	// 5. DeleteAutomationRule
	delOk, err := resolver.Mutation().DeleteAutomationRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.True(t, delOk)

	rulesAfter, err := resolver.Query().AutomationRules(ctx)
	require.NoError(t, err)
	assert.Empty(t, rulesAfter)

	// 6. Verify MutationEvents
	time.Sleep(50 * time.Millisecond)
	recorded := tracker.getEvents()
	require.True(t, len(recorded) >= 3)
	for _, ev := range recorded {
		assert.Equal(t, "automation", ev.Topic)
		assert.Equal(t, fam, ev.FamilyGroup)
	}
}

func TestSchemaResolvers_MultiTenantSecurity(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	households(t, resolver.DB, famA, famB, "default")

	// Member user belonging to Family A
	ctxMemberA := signedIn(t, resolver, famA, userA, "member")

	// V4-35: no argument names a household. Member A writes into Family A only.
	mem, err := resolver.Mutation().SaveTopicMemory(ctxMemberA, "health", "diet", "Gluten free", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, famA, mem.FamilyGroup)

	// An owner (an admin before V4-35, who could target any family) writes into their own only.
	ctxAdmin := signedIn(t, resolver, "default", "admin-usr", "admin")

	mem, err = resolver.Mutation().SaveTopicMemory(ctxAdmin, "health", "diet", "Gluten free", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, mem)
	assert.Equal(t, "default", mem.FamilyGroup)
	assert.NotEqual(t, famB, mem.FamilyGroup)
	var inB int64
	require.NoError(t, resolver.DB.DB.Table("topic_memories").Where("family_group = ?", famB).Count(&inB).Error)
	assert.Zero(t, inB, "nothing reaches Family B")
}

// BL-63: the mutation stores a rule's action as the chat tool does. A plain instruction on a
// cron rule is a prompt: (a turn for its person when it fires); on an event rule it would do
// nothing, so the rule is refused and nothing is stored.
func TestSchemaResolvers_SaveAutomationRule_APlainInstruction(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-" + uuid.New().String()
	households(t, resolver.DB, fam)
	ctx := signedIn(t, resolver, fam, "usr-"+uuid.New().String(), "member")

	rule, err := resolver.Mutation().SaveAutomationRule(ctx, "Digest", "cron", "*", `{"cron":"0 20 * * *"}`, "Send the evening digest", nil)
	require.NoError(t, err)
	assert.Equal(t, "prompt: Send the evening digest", rule.ActionInstruction)

	_, err = resolver.Mutation().SaveAutomationRule(ctx, "On spend", "event_mutation", "finance", "{}", "Tell me when Dana spends", nil)
	require.Error(t, err)
	rules, err := resolver.Query().AutomationRules(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 1, "the refused rule is not stored")
}
