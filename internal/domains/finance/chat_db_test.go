package finance_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatAs is the call context of a member's chat turn: their user row and the scope the
// production constructor builds from their membership.
func chatAs(t *testing.T, c *db.Client, u models.User) orchestrator.CallContext {
	t.Helper()
	s, err := identity.ForUser(context.Background(), c, &u)
	require.NoError(t, err)
	return orchestrator.CallContext{User: &u, Scope: s, DB: c, Extra: map[string]interface{}{}}
}

// BL-43: the chat tools store a false as asked, on create and on update.
func TestChat_FinanceStoresAFalseAsAsked(t *testing.T) {
	c, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	dana := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	call := chatAs(t, c, dana)
	mod := finance.NewModule(c)

	_, err := mod.Execute(ctx, call, "log_financial_transaction", map[string]interface{}{
		"amount": 80.0, "category": "health", "merchant": "Clinic BL43", "is_shared": false})
	require.NoError(t, err)
	var tx models.FinancialTransaction
	require.NoError(t, c.DB.Where("household_id = ? AND merchant = ?", dana.HouseholdID, "Clinic BL43").First(&tx).Error)
	assert.False(t, tx.IsShared, "a transaction logged unshared is stored unshared")

	_, err = mod.Execute(ctx, call, "manage_recurring_bills", map[string]interface{}{
		"action": "add", "name": "Gym BL43", "expected_amount": 50.0, "auto_renew": false})
	require.NoError(t, err)
	var bill models.RecurringBill
	require.NoError(t, c.DB.Where("household_id = ? AND name = ?", dana.HouseholdID, "Gym BL43").First(&bill).Error)
	assert.False(t, bill.AutoRenew, "created without auto-renewal")

	for _, want := range []bool{true, false} {
		_, err = mod.Execute(ctx, call, "manage_recurring_bills", map[string]interface{}{
			"action": "update", "bill_id": float64(bill.ID), "auto_renew": want})
		require.NoError(t, err)
		var got models.RecurringBill
		require.NoError(t, c.DB.First(&got, bill.ID).Error)
		assert.Equal(t, want, got.AutoRenew, "updated")
	}
}

// BL-44 (owner's decision): in chat a member sees their own unshared transactions and the
// household's shared ones, the rule the dashboard has had since V4-35. Two members of one
// household each log one of each; each one's summary counts only what they may see.
func TestChat_AMemberSeesSharedAndTheirOwnTransactions(t *testing.T) {
	c, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	dana := chatAs(t, c, householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram"))
	omer := chatAs(t, c, householdtest.Member(t, c, "1002-"+levi, "Omer", "family", levi, "telegram"))
	mod := finance.NewModule(c)

	logTx := func(call orchestrator.CallContext, merchant string, amount float64, shared bool) {
		_, err := mod.Execute(ctx, call, "log_financial_transaction", map[string]interface{}{
			"amount": amount, "category": "health", "merchant": merchant, "is_shared": shared})
		require.NoError(t, err)
	}
	logTx(dana, "Dana shared", 10, true)
	logTx(dana, "Dana private", 20, false)
	logTx(omer, "Omer shared", 100, true)
	logTx(omer, "Omer private", 1000, false)

	summary := func(call orchestrator.CallContext) string {
		out, err := mod.Execute(ctx, call, "get_financial_summary", map[string]interface{}{})
		require.NoError(t, err)
		return out
	}
	got := summary(dana)
	assert.Contains(t, got, "Total Expenses: $130.00", "Dana: her own two and Omer's shared one")
	assert.Contains(t, got, "Total Transactions: 3")
	assert.NotContains(t, got, "1000.00")
	assert.NotContains(t, got, "1100.00", "Omer's unshared spending is not in his total either")

	got = summary(omer)
	assert.Contains(t, got, "Total Expenses: $1110.00", "Omer: his own two and Dana's shared one")
	assert.Contains(t, got, "Total Transactions: 3")
	assert.NotContains(t, got, "$30.00", "Dana's unshared spending is not in her total")

	// Asking for another member by name reaches only what they shared.
	out, err := mod.Execute(ctx, dana, "get_financial_summary", map[string]interface{}{"user_id": "1002-" + levi})
	require.NoError(t, err)
	assert.Contains(t, out, "Total Expenses: $100.00")
}
