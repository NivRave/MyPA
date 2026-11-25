package durability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-68, the owner's decision 3: entries written before the fix under the old action names are
// undone where their stored snapshot is enough; otherwise /undo says plainly which change it
// cannot undo and stops. It never undoes an older change instead.

// oldEntry records a mutation as the chat tools logged it before BL-68.
func oldEntry(t *testing.T, c *db.Client, s identity.Scope, person, action, entityType, entityID, before, after, summary string, at time.Time) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, c.InsertMutationLog(s, models.MutationLog{ID: id, UserID: person, Topic: "finance", Action: action,
		EntityType: entityType, EntityID: entityID, BeforeJSON: before, AfterJSON: after, Summary: summary, CreatedAt: at}))
	return id
}

func TestUndo_AnOldEntryWithAUsableSnapshotIsUndone(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	s := householdtest.Scope(t, client, "undo_old_ok", "member_o", "family")
	now := time.Now()

	t.Run("transaction_logged: the transaction is removed", func(t *testing.T) {
		tx := models.FinancialTransaction{UserID: "member_o", HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(),
			Type: models.TransactionTypeExpense, Category: models.CategoryGroceries, Amount: 42, Currency: "USD",
			Merchant: "Market", TransactionDate: now, IsShared: true}
		require.NoError(t, client.DB.Create(&tx).Error)
		after, _ := json.Marshal(tx)
		id := oldEntry(t, client, s, "member_o", "transaction_logged", "FinancialTransaction", fmt.Sprint(tx.ID), "", string(after),
			"Logged expense USD 42.00 for groceries (Market)", now)

		res, err := module.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.Equal(t, id, res.MutationID)
		assert.False(t, rowExists(t, client, "financial_transactions", fmt.Sprint(tx.ID)))
		assert.True(t, isUndone(t, client, s, id))
	})

	t.Run("recurring_bill_deleted: the bill comes back from the after-image the old entry kept", func(t *testing.T) {
		bill := models.RecurringBill{HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(), Name: "Netflix",
			Category: models.CategoryOther, ExpectedAmount: 50, Currency: "USD", BillingCycle: models.BillingCycleMonthly, NextDueDay: 3}
		require.NoError(t, client.DB.Create(&bill).Error)
		var stored models.RecurringBill
		require.NoError(t, client.DB.First(&stored, bill.ID).Error)
		after, _ := json.Marshal(stored)
		require.NoError(t, client.DB.Delete(&models.RecurringBill{}, bill.ID).Error)
		id := oldEntry(t, client, s, "member_o", "recurring_bill_deleted", "RecurringBill", fmt.Sprint(bill.ID), "", string(after),
			"Deleted recurring bill Netflix", now.Add(time.Second))

		res, err := module.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.Equal(t, id, res.MutationID)
		var back models.RecurringBill
		require.NoError(t, client.DB.First(&back, bill.ID).Error)
		assert.Equal(t, "Netflix", back.Name)
		assert.Equal(t, s.HouseholdID(), back.HouseholdID)
	})

	t.Run("maintenance_updated: the old values come back", func(t *testing.T) {
		item := models.HomeMaintenanceItem{HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(), TaskName: "Filters",
			Category: "hvac", IntervalDays: 90, NextDueAt: now}
		require.NoError(t, client.DB.Create(&item).Error)
		var stored models.HomeMaintenanceItem
		require.NoError(t, client.DB.First(&stored, item.ID).Error)
		before, _ := json.Marshal(stored)
		require.NoError(t, client.DB.Model(&stored).Update("interval_days", 30).Error)
		id := oldEntry(t, client, s, "member_o", "maintenance_updated", "HomeMaintenanceItem", fmt.Sprint(item.ID), string(before), "",
			"Updated home maintenance task: Filters", now.Add(2*time.Second))

		res, err := module.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.Equal(t, id, res.MutationID)
		var back models.HomeMaintenanceItem
		require.NoError(t, client.DB.First(&back, item.ID).Error)
		assert.Equal(t, 90, back.IntervalDays)
	})
}

// A rule's own record that it ran (rule_executed, on its AutomationRule, under the rule's person)
// changed no row: /undo right after a change that fired the person's own rule undoes that change.
func TestUndo_ARulesRecordThatItRanDoesNotBlockUndo(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	s := householdtest.Scope(t, client, "undo_rule_ran", "member_r", "family")
	now := time.Now()

	feeding := sharedRow(t, client, s, "member_r")
	change := logChange(t, client, s, "member_r", "create", "BabyCareLog", feeding, "", now)
	for i, action := range []string{"rule_executed", "rule_execution_failed"} {
		logChange(t, client, s, "member_r", action, "AutomationRule", "1", "", now.Add(time.Duration(i+1)*time.Second))
	}

	res, err := module.RollbackLast(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, change, res.MutationID, "the change that fired the rule is undone")
	assert.False(t, res.NotUndoable, res.Detail)
	assert.False(t, rowExists(t, client, "baby_care_logs", feeding))
}

// An update entry written before BL-68 kept the whole row but left out its empty fields: undo
// clears what the change set in them, and the row is exactly as before.
func TestUndo_AnOldWholeRowSnapshotClearsTheFieldsItLeftOut(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	s := householdtest.Scope(t, client, "undo_old_omitted", "member_v", "family")

	car := models.VehicleAsset{HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(), Name: "Fiat", CurrentMileageKm: 20000}
	require.NoError(t, client.DB.Create(&car).Error)
	var stored models.VehicleAsset
	require.NoError(t, client.DB.First(&stored, car.ID).Error)
	require.Nil(t, stored.LastServiceDate)
	before, _ := json.Marshal(stored) // as the old code wrote it: no last_service_date at all
	require.NotContains(t, string(before), "last_service_date")
	served := time.Now()
	require.NoError(t, client.DB.Model(&stored).Updates(map[string]interface{}{"last_service_date": served, "current_mileage_km": 21000}).Error)
	id := oldEntry(t, client, s, "member_v", "vehicle_service_logged", "VehicleAsset", fmt.Sprint(car.ID), string(before), "",
		"Logged service for Fiat at 21000 km", time.Now())

	res, err := module.RollbackLast(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, id, res.MutationID)
	var back models.VehicleAsset
	require.NoError(t, client.DB.First(&back, car.ID).Error)
	assert.Equal(t, 20000, back.CurrentMileageKm)
	assert.Nil(t, back.LastServiceDate, "the service date the change set is cleared again")
}

// From the dashboard an owner may look at another member's old entries; one that cannot be undone
// is named without its summary, and one that touched the member's own data is refused, never
// described.
func TestUndo_AnOwnerIsNotShownAnotherMembersSummary(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	owner := householdtest.Scope(t, client, "undo_old_owner", "owner_w", "admin")
	member := householdtest.Scope(t, client, "undo_old_owner", "member_w", "family")
	now := time.Now()

	budget := models.CategoryBudget{HouseholdID: owner.HouseholdID(), FamilyGroup: owner.LegacyLabel(), Category: models.CategoryDining,
		MonthlyLimit: 600, Currency: "USD", AlertThresholdPct: 80}
	require.NoError(t, client.DB.Create(&budget).Error)
	shared := oldEntry(t, client, member, "member_w", "budget_set", "CategoryBudget", fmt.Sprint(budget.ID), "", `{"monthly_limit":600}`,
		"Set dining monthly budget to USD 600.00 (alert at 80%)", now)
	res, err := module.RollbackFromDashboard(ctx, owner, shared)
	require.NoError(t, err)
	assert.True(t, res.NotUndoable)
	assert.NotContains(t, res.Detail, "USD 600.00", "another member's summary is not repeated")
	assert.Contains(t, res.Detail, "Nothing was changed")

	mem := models.TopicMemory{UserID: "member_w", HouseholdID: member.HouseholdID(), FamilyGroup: member.LegacyLabel(), Topic: "health",
		Fact: "Sees a therapist on Tuesdays", Scope: "personal", CreatedByUserID: "member_w", TagsJSON: "[]"}
	require.NoError(t, client.DB.Create(&mem).Error)
	after, _ := json.Marshal(mem)
	private := oldEntry(t, client, member, "member_w", "supersede", "TopicMemory", fmt.Sprint(mem.ID), "", string(after),
		"Saved memory [health] : Sees a therapist on Tuesdays", now)
	_, err = module.RollbackFromDashboard(ctx, owner, private)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	assert.NotContains(t, err.Error(), "therapist")
}

func TestUndo_AnOldEntryWithoutASnapshotIsNamedAndNothingChanges(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	s := householdtest.Scope(t, client, "undo_old_no", "member_n", "family")
	now := time.Now()

	// An older change of the person's that /undo could reverse.
	feeding := sharedRow(t, client, s, "member_n")
	older := logChange(t, client, s, "member_n", "create", "BabyCareLog", feeding, "", now.Add(-time.Minute))

	// budget_set wrote only the budget as it was after: whether it was new, and what it was before,
	// is not known.
	budget := models.CategoryBudget{HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(), Category: models.CategoryDining,
		MonthlyLimit: 600, Currency: "USD", AlertThresholdPct: 80}
	require.NoError(t, client.DB.Create(&budget).Error)
	after, _ := json.Marshal(budget)
	stuck := oldEntry(t, client, s, "member_n", "budget_set", "CategoryBudget", fmt.Sprint(budget.ID), "", string(after),
		"Set dining monthly budget to USD 600.00 (alert at 80%)", now)

	for i := 0; i < 2; i++ {
		res, err := module.RollbackLast(ctx, s)
		require.NoError(t, err, "the answer is a plain message, not a failure")
		assert.Equal(t, stuck, res.MutationID, "it names the newest change, the one it cannot undo")
		assert.Contains(t, res.Detail, "Set dining monthly budget to USD 600.00", "it says which change")
		assert.Contains(t, res.Detail, "Nothing was changed")
		assert.NotContains(t, res.Detail, "unsupported")

		var got models.CategoryBudget
		require.NoError(t, client.DB.First(&got, budget.ID).Error)
		assert.Equal(t, 600.0, got.MonthlyLimit, "the budget is left as it is")
		assert.False(t, isUndone(t, client, s, stuck), "the entry is not marked undone")
		assert.True(t, rowExists(t, client, "baby_care_logs", feeding), "the older change is not undone instead")
		assert.False(t, isUndone(t, client, s, older))
	}
}
