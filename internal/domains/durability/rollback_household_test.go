package durability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-30: a rollback acts only inside the caller's household, and a restored snapshot always lands
// there, whatever the snapshot says.

// A delete snapshot written before V4-30 carries no household_id. Undoing it restores the row into
// the caller's household, with the household's label in family_group.
func TestRollback_PreV430DeleteSnapshotLandsInTheCallersHousehold(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	scope := householdtest.Scope(t, client, "pre_v430_family", "pre_v430_actor", "family")

	start := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	before, err := json.Marshal(map[string]interface{}{
		"id":                 777001,
		"user_id":            "pre_v430_actor",
		"family_group":       "pre_v430_family",
		"topic":              "schedule",
		"title":              "Dentist",
		"start_time":         start.Format(time.RFC3339),
		"end_time":           start.Add(time.Hour).Format(time.RFC3339),
		"created_by_user_id": "pre_v430_actor",
	})
	require.NoError(t, err)
	mutID := uuid.New().String()
	require.NoError(t, client.InsertMutationLog(scope, models.MutationLog{
		ID: mutID, Topic: "schedule", Action: "delete", EntityType: "ScheduleEvent", EntityID: "777001",
		UserID: "pre_v430_actor", BeforeJSON: string(before), Summary: "Deleted dentist",
	}))

	_, err = module.Rollback(ctx, scope, mutID)
	require.NoError(t, err)

	var restored models.ScheduleEvent
	require.NoError(t, client.DB.Where("id = ?", 777001).First(&restored).Error)
	assert.Equal(t, scope.HouseholdID(), restored.HouseholdID, "the restored row belongs to the caller's household")
	assert.Equal(t, "pre_v430_family", restored.FamilyGroup)
	assert.Equal(t, "Dentist", restored.Title)
}

// A mutation of household A cannot be undone with household B's scope, by ID or as B's latest,
// nor through the repository directly; the row and the log are unchanged.
func TestRollback_AnotherHouseholdsMutationIsRefused(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	scopeA := householdtest.Scope(t, client, "household_a", "actor_a", "family")
	scopeB := householdtest.Scope(t, client, "household_b", "actor_b", "admin")

	row := models.BabyCareLog{BabyID: 1, HouseholdID: scopeA.HouseholdID(), FamilyGroup: "household_a", LogType: "feeding", Timestamp: time.Now(), LoggedByUserID: "actor_a"}
	require.NoError(t, client.DB.Create(&row).Error)
	mutID := uuid.New().String()
	require.NoError(t, client.InsertMutationLog(scopeA, models.MutationLog{
		ID: mutID, Topic: "baby", Action: "create", EntityType: "BabyCareLog", EntityID: fmt.Sprint(row.ID),
		UserID: "actor_a", Summary: "Logged feeding",
	}))

	res, err := module.Rollback(ctx, scopeB, mutID)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "permission denied")

	_, err = module.RollbackLast(ctx, scopeB)
	require.Error(t, err, "household B has no mutation of its own to undo")

	logA, err := client.GetMutationLogByID(scopeA, mutID)
	require.NoError(t, err)
	require.Error(t, client.RollbackSnapshot(ctx, scopeB, *logA, "baby_care_logs", models.UndoViaDashboard), "the repository refuses another household's log")

	_, err = client.GetMutationLogByID(scopeB, mutID)
	require.Error(t, err, "household B cannot read household A's log")

	var count int64
	require.NoError(t, client.DB.Model(&models.BabyCareLog{}).Where("id = ?", row.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "household A's row is untouched")
	logA, err = client.GetMutationLogByID(scopeA, mutID)
	require.NoError(t, err)
	assert.False(t, logA.IsUndone, "household A's log is not marked undone")
}

// A crafted update snapshot that names another household, another label and another ID restores
// the values but not the move: the row stays in the caller's household, under the same ID.
func TestRollback_CraftedUpdateSnapshotDoesNotMoveTheRow(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	scopeA := householdtest.Scope(t, client, "crafted_a", "actor_a", "family")
	scopeB := householdtest.Scope(t, client, "crafted_b", "actor_b", "family")

	tx := models.FinancialTransaction{UserID: "actor_a", HouseholdID: scopeA.HouseholdID(), FamilyGroup: "crafted_a",
		Type: models.TransactionTypeExpense, Category: models.CategoryGroceries, Amount: 150, Currency: "USD", Merchant: "Shop", TransactionDate: time.Now()}
	require.NoError(t, client.DB.Create(&tx).Error)

	before, err := json.Marshal(map[string]interface{}{
		"id":           tx.ID + 1000,
		"amount":       90.0,
		"household_id": scopeB.HouseholdID(),
		"family_group": "crafted_b",
	})
	require.NoError(t, err)
	mutID := uuid.New().String()
	require.NoError(t, client.InsertMutationLog(scopeA, models.MutationLog{
		ID: mutID, Topic: "finance", Action: "update", EntityType: "FinancialTransaction", EntityID: fmt.Sprint(tx.ID),
		UserID: "actor_a", BeforeJSON: string(before), Summary: "Edited amount",
	}))

	_, err = module.Rollback(ctx, scopeA, mutID)
	require.NoError(t, err)

	var got models.FinancialTransaction
	require.NoError(t, client.DB.Where("id = ?", tx.ID).First(&got).Error, "the row keeps its ID")
	assert.Equal(t, 90.0, got.Amount, "the snapshot's values are restored")
	assert.Equal(t, scopeA.HouseholdID(), got.HouseholdID, "the row stays in the caller's household")
	assert.Equal(t, "crafted_a", got.FamilyGroup)
	var inB int64
	require.NoError(t, client.DB.Model(&models.FinancialTransaction{}).Where("household_id = ?", scopeB.HouseholdID()).Count(&inB).Error)
	assert.Zero(t, inB, "nothing landed in the other household")
}

// A log of household A that names a row of household B (an entity ID guessed or reused) cannot
// touch that row: the rollback fails and the log stays active.
func TestRollback_LogCannotReachAnotherHouseholdsRow(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	scopeA := householdtest.Scope(t, client, "reach_a", "actor_a", "family")
	scopeB := householdtest.Scope(t, client, "reach_b", "actor_b", "family")

	rowB := models.BabyCareLog{BabyID: 1, HouseholdID: scopeB.HouseholdID(), FamilyGroup: "reach_b", LogType: "diaper", Timestamp: time.Now(), LoggedByUserID: "actor_b"}
	require.NoError(t, client.DB.Create(&rowB).Error)
	mutID := uuid.New().String()
	require.NoError(t, client.InsertMutationLog(scopeA, models.MutationLog{
		ID: mutID, Topic: "baby", Action: "create", EntityType: "BabyCareLog", EntityID: fmt.Sprint(rowB.ID),
		UserID: "actor_a", Summary: "Logged diaper",
	}))

	_, err := module.Rollback(ctx, scopeA, mutID)
	require.Error(t, err)

	var count int64
	require.NoError(t, client.DB.Model(&models.BabyCareLog{}).Where("id = ?", rowB.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "household B's row is untouched")
	logA, err := client.GetMutationLogByID(scopeA, mutID)
	require.NoError(t, err)
	assert.False(t, logA.IsUndone)
}
