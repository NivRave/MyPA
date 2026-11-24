package graph

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	dbmodels "github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// undoResolver is a resolver whose mutations are logged by the real durability module, as in the
// running app, and that module (/undo in chat goes to it too).
func undoResolver(t *testing.T) (*Resolver, *durability.Module) {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	bus := events.NewEventBus()
	dur := durability.NewModule(client, bus)
	return NewResolver(ResolverConfig{DB: client, EventBus: bus, Durability: dur}), dur
}

// BL-47: a household settings change stays off the mutation log, so /undo after it still undoes
// the previous change that can be undone.
func TestUndo_HouseholdSettingsChangeDoesNotBlockUndo(t *testing.T) {
	r, dur := undoResolver(t)
	fam := "fam-undo-settings-" + uuid.NewString()
	owner := scopeFor(t, r, fam, "owner-"+uuid.NewString(), "admin")
	ctx := WithCaller(context.Background(), owner)

	profile, err := r.Mutation().UpsertBabyProfile(ctx, "Noah", time.Now().Add(-30*24*time.Hour), "male", nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = r.Mutation().UpdateHousehold(ctx, ptr("The Levis"), nil, ptr("Asia/Jerusalem"), nil)
	require.NoError(t, err)

	var settings int64
	require.NoError(t, r.DB.DB.Model(&dbmodels.MutationLog{}).Where("household_id = ? AND topic = ?", owner.HouseholdID(), "household").Count(&settings).Error)
	assert.Zero(t, settings, "a settings change is not on the mutation log")

	res, err := dur.RollbackLast(context.Background(), owner) // /undo in chat
	require.NoError(t, err, "/undo still works after a settings change")
	assert.Equal(t, profile.ID, res.EntityID, "it undoes the previous undoable change")
	var n int64
	require.NoError(t, r.DB.DB.Model(&dbmodels.BabyProfile{}).Where("id = ?", profile.ID).Count(&n).Error)
	assert.Zero(t, n)
}

// undoFixture is one household with an owner and two adults, B and C.
type undoFixture struct {
	r                *Resolver
	owner, b, c      identity.Scope
	ownerK, bK, cK   string
	ownerC, bC, cCtx context.Context
}

func newUndoFixture(t *testing.T) undoFixture {
	t.Helper()
	r, _ := undoResolver(t)
	fam := "fam-undo-roles-" + uuid.NewString()
	f := undoFixture{r: r, ownerK: "owner-" + uuid.NewString(), bK: "adult-b-" + uuid.NewString(), cK: "adult-c-" + uuid.NewString()}
	f.owner = scopeFor(t, r, fam, f.ownerK, "admin")
	f.b = scopeFor(t, r, fam, f.bK, "member")
	f.c = scopeFor(t, r, fam, f.cK, "member")
	f.ownerC, f.bC, f.cCtx = WithCaller(context.Background(), f.owner), WithCaller(context.Background(), f.b), WithCaller(context.Background(), f.c)
	return f
}

// change records a mutation by userID of a row in the household, as the module logs it.
func (f undoFixture) change(t *testing.T, userID, entityType, entityID string) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, f.r.DB.InsertMutationLog(f.owner, dbmodels.MutationLog{ID: id, UserID: userID, Topic: "test", Action: "create",
		EntityType: entityType, EntityID: entityID, Summary: "created " + entityType, CreatedAt: time.Now()}))
	return id
}

func (f undoFixture) shared(t *testing.T, by string) string {
	t.Helper()
	row := dbmodels.BabyCareLog{BabyID: 1, HouseholdID: f.owner.HouseholdID(), FamilyGroup: f.owner.LegacyLabel(), LogType: "feeding",
		Timestamp: time.Now(), LoggedByUserID: by}
	require.NoError(t, f.r.DB.DB.Create(&row).Error)
	return fmt.Sprint(row.ID)
}

func (f undoFixture) workflow(t *testing.T, s identity.Scope, person string) string {
	t.Helper()
	rule := &dbmodels.AutomationRule{UserID: person, Name: "Morning summary", TriggerType: "cron", TriggerTopic: "*",
		ConditionJSON: `{"cron": "0 7 * * *"}`, ActionInstruction: "prompt: Summarise my day", IsActive: true}
	require.NoError(t, f.r.DB.CreateAutomationRule(s, rule))
	return fmt.Sprint(rule.ID)
}

func (f undoFixture) privateTx(t *testing.T, person string) string {
	t.Helper()
	tx := dbmodels.FinancialTransaction{UserID: person, HouseholdID: f.owner.HouseholdID(), FamilyGroup: f.owner.LegacyLabel(),
		Type: dbmodels.TransactionTypeExpense, Category: dbmodels.CategoryGroceries, Amount: 12, Currency: "USD", TransactionDate: time.Now()}
	require.NoError(t, f.r.DB.DB.Create(&tx).Error)
	return fmt.Sprint(tx.ID)
}

func (f undoFixture) exists(t *testing.T, table, id string) bool {
	t.Helper()
	var n int64
	require.NoError(t, f.r.DB.DB.Table(table).Where("id = ?", id).Count(&n).Error)
	return n == 1
}

func refused(t *testing.T, ctx context.Context, r *Resolver, mutationID, why string) {
	t.Helper()
	ok, err := r.Mutation().RollbackMutation(ctx, mutationID)
	require.Error(t, err, why)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "permission denied", why)
}

func undone(t *testing.T, ctx context.Context, r *Resolver, mutationID, why string) {
	t.Helper()
	ok, err := r.Mutation().RollbackMutation(ctx, mutationID)
	require.NoError(t, err, why)
	assert.True(t, ok, why)
}

// BL-65, the dashboard: an owner may roll back any change to the household's shared data; a change
// to a person-owned row only that person may roll back; a change with no known person only an
// owner, and only to shared data. An adult rolls back their own changes.
func TestUndo_DashboardRulesPerRole(t *testing.T) {
	t.Run("an owner rolls back another member's change to shared data; another adult may not", func(t *testing.T) {
		f := newUndoFixture(t)
		row := f.shared(t, f.bK)
		id := f.change(t, f.bK, "BabyCareLog", row)
		refused(t, f.cCtx, f.r, id, "an adult may not roll back another adult's change")
		assert.True(t, f.exists(t, "baby_care_logs", row))
		undone(t, f.ownerC, f.r, id, "an owner may roll back a change to shared data")
		assert.False(t, f.exists(t, "baby_care_logs", row))
	})

	t.Run("an adult rolls back their own change to shared data", func(t *testing.T) {
		f := newUndoFixture(t)
		row := f.shared(t, f.bK)
		undone(t, f.bC, f.r, f.change(t, f.bK, "BabyCareLog", row), "one's own change")
		assert.False(t, f.exists(t, "baby_care_logs", row))
	})

	t.Run("a person-owned workflow is rolled back by its person only", func(t *testing.T) {
		f := newUndoFixture(t)
		wf := f.workflow(t, f.b, f.bK)
		id := f.change(t, f.bK, "AutomationRule", wf)
		refused(t, f.ownerC, f.r, id, "an owner may not roll back a person's own workflow")
		refused(t, f.cCtx, f.r, id, "nor may another adult")
		assert.True(t, f.exists(t, "automation_rules", wf))
		undone(t, f.bC, f.r, id, "its person may")
		assert.False(t, f.exists(t, "automation_rules", wf))
	})

	t.Run("a private transaction is rolled back by its person only", func(t *testing.T) {
		f := newUndoFixture(t)
		tx := f.privateTx(t, f.bK)
		id := f.change(t, f.bK, "FinancialTransaction", tx)
		refused(t, f.ownerC, f.r, id, "an owner may not roll back a member's private transaction")
		assert.True(t, f.exists(t, "financial_transactions", tx))
		undone(t, f.bC, f.r, id, "its person may")
	})

	t.Run("a change with no known person: an owner, shared data only", func(t *testing.T) {
		f := newUndoFixture(t)
		row := f.shared(t, "dashboard-service")
		id := f.change(t, "dashboard-service", "BabyCareLog", row)
		refused(t, f.bC, f.r, id, "an adult may not roll back a change with no known person")
		assert.True(t, f.exists(t, "baby_care_logs", row))
		undone(t, f.ownerC, f.r, id, "an owner may, for shared data")
		assert.False(t, f.exists(t, "baby_care_logs", row))

		wf := f.workflow(t, f.b, f.bK)
		wfID := f.change(t, "", "AutomationRule", wf)
		refused(t, f.ownerC, f.r, wfID, "not even an owner, for a person-owned row")
		refused(t, f.bC, f.r, wfID, "nor its person: the change is not theirs")
		assert.True(t, f.exists(t, "automation_rules", wf))
	})

	t.Run("in chat an owner undoes only their own changes", func(t *testing.T) {
		f := newUndoFixture(t)
		row := f.shared(t, f.bK)
		id := f.change(t, f.bK, "BabyCareLog", row)
		dur := f.r.durabilityEngine()
		_, err := dur.Rollback(context.Background(), f.owner, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
		assert.True(t, f.exists(t, "baby_care_logs", row))
	})
}
