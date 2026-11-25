package finance_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-66 (the owner's decision 5): a person changes their own transaction (shared or private,
// amount, category, merchant, date) and deletes it, in chat; no one else can, an owner included.
// Both are announced and undoable like other changes; a transaction made private leaves the other
// members' lists and totals, and neither the partner notifier nor a household rule reveals it.

// editHousehold is the Levi household: Dana, an owner, and Omer and Noa, adults, writing in chat,
// with the durability module logging every change on one bus.
type editHousehold struct {
	c               *db.Client
	mod             *finance.Module
	dur             *durability.Module
	bus             *events.EventBus
	dana, omer, noa orchestrator.CallContext

	mu  sync.Mutex
	evs []events.MutationEvent
}

func newEditHousehold(t *testing.T) *editHousehold {
	t.Helper()
	c, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	bus := events.NewEventBus()
	t.Cleanup(func() { _ = bus.Close() })
	h := &editHousehold{c: c, mod: finance.NewModule(c), dur: durability.NewModule(c, bus), bus: bus}
	bus.SubscribeSync(func(_ context.Context, ev events.MutationEvent) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.evs = append(h.evs, ev)
		return nil
	})
	levi := "Levi-" + uuid.NewString()
	as := func(id, name, role string) orchestrator.CallContext {
		call := chatAs(t, c, householdtest.Member(t, c, id+"-"+levi, name, role, levi, "telegram"))
		call.EventBus = bus
		return call
	}
	h.dana, h.omer, h.noa = as("1001", "Dana", "admin"), as("1002", "Omer", "family"), as("1003", "Noa", "family")
	return h
}

func (h *editHousehold) events() []events.MutationEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]events.MutationEvent(nil), h.evs...)
}

func (h *editHousehold) logTx(t *testing.T, call orchestrator.CallContext, merchant string, amount float64, shared bool) uint {
	t.Helper()
	_, err := h.mod.Execute(context.Background(), call, "log_financial_transaction", map[string]interface{}{
		"amount": amount, "category": "groceries", "merchant": merchant, "is_shared": shared})
	require.NoError(t, err)
	var tx models.FinancialTransaction
	require.NoError(t, h.c.DB.Where("household_id = ? AND merchant = ?", call.Scope.HouseholdID(), merchant).First(&tx).Error)
	return tx.ID
}

func (h *editHousehold) tx(t *testing.T, id uint) (models.FinancialTransaction, bool) {
	t.Helper()
	var tx models.FinancialTransaction
	err := h.c.DB.Where("id = ?", id).Limit(1).Find(&tx).Error
	require.NoError(t, err)
	return tx, tx.ID == id
}

func (h *editHousehold) summary(t *testing.T, call orchestrator.CallContext) string {
	t.Helper()
	out, err := h.mod.Execute(context.Background(), call, "get_financial_summary", map[string]interface{}{})
	require.NoError(t, err)
	return out
}

func (h *editHousehold) visibleTo(t *testing.T, call orchestrator.CallContext, id uint) bool {
	t.Helper()
	txs, err := h.c.ListTransactions(context.Background(), call.Scope, db.TransactionFilter{})
	require.NoError(t, err)
	for _, tx := range txs {
		if tx.ID == id {
			return true
		}
	}
	return false
}

func TestChat_APersonChangesAndDeletesTheirOwnTransaction(t *testing.T) {
	h := newEditHousehold(t)
	ctx := context.Background()
	id := h.logTx(t, h.omer, "Market BL66", 120, true)
	original, _ := h.tx(t, id)
	require.True(t, h.visibleTo(t, h.dana, id), "shared: Dana sees it")
	require.Contains(t, h.summary(t, h.dana), "Total Expenses: $120.00")

	// Omer changes everything and makes it private.
	out, err := h.mod.Execute(ctx, h.omer, "log_financial_transaction", map[string]interface{}{
		"action": "update", "transaction_id": float64(id), "amount": 150.0, "category": "dining", "merchant": "Bistro BL66",
		"transaction_date": "2026-11-20", "is_shared": false})
	require.NoError(t, err)
	assert.Contains(t, out, "150.00")
	got, ok := h.tx(t, id)
	require.True(t, ok)
	assert.Equal(t, 150.0, got.Amount)
	assert.Equal(t, models.CategoryDining, got.Category)
	assert.Equal(t, "Bistro BL66", got.Merchant)
	assert.Equal(t, "2026-11-20", got.TransactionDate.UTC().Format("2006-01-02"))
	assert.False(t, got.IsShared)

	assert.False(t, h.visibleTo(t, h.dana, id), "private: gone from Dana's list")
	assert.False(t, h.visibleTo(t, h.noa, id), "and from Noa's")
	assert.NotContains(t, h.summary(t, h.dana), "150.00", "and from their totals")
	assert.True(t, h.visibleTo(t, h.omer, id), "Omer still sees his own")

	last := h.events()[len(h.events())-1]
	assert.Equal(t, "update", last.Action, "announced as an update")
	assert.Equal(t, "FinancialTransaction", last.EntityType)

	// /undo puts it back as it was, shared again.
	res, err := h.dur.RollbackLast(ctx, h.omer.Scope)
	require.NoError(t, err)
	assert.Equal(t, last.ID, res.MutationID)
	back, _ := h.tx(t, id)
	assert.Equal(t, original.Amount, back.Amount)
	assert.Equal(t, original.Merchant, back.Merchant)
	assert.True(t, back.IsShared)
	assert.True(t, h.visibleTo(t, h.dana, id))

	// Omer deletes it; /undo brings it back.
	_, err = h.mod.Execute(ctx, h.omer, "log_financial_transaction", map[string]interface{}{"action": "delete", "transaction_id": float64(id)})
	require.NoError(t, err)
	_, ok = h.tx(t, id)
	assert.False(t, ok, "deleted")
	last = h.events()[len(h.events())-1]
	assert.Equal(t, "delete", last.Action, "announced as a delete")
	res, err = h.dur.RollbackLast(ctx, h.omer.Scope)
	require.NoError(t, err)
	assert.Equal(t, last.ID, res.MutationID)
	back, ok = h.tx(t, id)
	require.True(t, ok, "/undo brings it back")
	assert.Equal(t, "Market BL66", back.Merchant)
}

// After a change, budgets and summaries use the new values.
func TestChat_AChangedTransactionCountsWithItsNewValues(t *testing.T) {
	h := newEditHousehold(t)
	ctx := context.Background()
	_, err := h.mod.Execute(ctx, h.omer, "set_category_budget", map[string]interface{}{"category": "dining", "monthly_limit": 100.0})
	require.NoError(t, err)
	id := h.logTx(t, h.omer, "Cafe BL66", 40, true)

	out, err := h.mod.Execute(ctx, h.omer, "log_financial_transaction", map[string]interface{}{
		"action": "update", "transaction_id": float64(id), "category": "dining", "amount": 130.0})
	require.NoError(t, err)
	assert.Contains(t, out, "BUDGET EXCEEDED", "the budget check uses the new amount and category")
	sum := h.summary(t, h.omer)
	assert.Contains(t, sum, "Total Expenses: $130.00")
	assert.Contains(t, sum, "**dining**: Spent $130.00")
}

func TestChat_OnlyItsPersonMayChangeOrDeleteATransaction(t *testing.T) {
	h := newEditHousehold(t)
	ctx := context.Background()
	id := h.logTx(t, h.omer, "Pharmacy BL66", 60, true)
	before, _ := h.tx(t, id)
	mark := len(h.events())

	for _, who := range []struct {
		name string
		call orchestrator.CallContext
	}{{"an owner", h.dana}, {"another adult", h.noa}} {
		_, err := h.mod.Execute(ctx, who.call, "log_financial_transaction", map[string]interface{}{
			"action": "update", "transaction_id": float64(id), "amount": 1.0, "is_shared": false})
		assert.Error(t, err, "%s may not change it", who.name)
		_, err = h.mod.Execute(ctx, who.call, "log_financial_transaction", map[string]interface{}{
			"action": "delete", "transaction_id": float64(id)})
		assert.Error(t, err, "%s may not delete it", who.name)
	}
	after, ok := h.tx(t, id)
	require.True(t, ok)
	assert.Equal(t, before.Amount, after.Amount)
	assert.True(t, after.IsShared)
	assert.Len(t, h.events(), mark, "a refused change publishes nothing")
}

// A transaction made private is neither announced by the partner notifier nor seen by another
// member's household rule; a shared one still is, as before.
func TestChat_APrivateTransactionIsNotRevealedByTheNotifierOrARule(t *testing.T) {
	h := newEditHousehold(t)
	ctx := context.Background()
	require.NoError(t, h.c.UpsertSharedSpace(h.dana.Scope, &models.SharedSpace{Topic: "finance", OwnerUserID: h.dana.ActorID(),
		CanView: true, CanEdit: true, NotifyMode: models.NotifyModeInstant}))
	require.NoError(t, h.c.CreateAutomationRule(h.dana.Scope, &models.AutomationRule{UserID: h.dana.ActorID(), Name: "Big spend",
		TriggerType: "event_mutation", TriggerTopic: "finance", ConditionJSON: `{"min_amount": 100}`,
		ActionInstruction: "notify: a big expense", IsActive: true}))

	var mu sync.Mutex
	var sent []string
	send := func(_ context.Context, to, text string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, to+": "+text)
		return nil
	}
	notifier := shared.NewNotifier(h.c, send)
	runner := automation.NewRunner(h.c, nil, automation.WithNotificationSender(send))
	deliver := func(evs []events.MutationEvent) []string {
		mu.Lock()
		sent = nil
		mu.Unlock()
		for _, ev := range evs {
			require.NoError(t, notifier.HandleMutationEvent(ctx, ev))
			require.NoError(t, runner.HandleMutationEvent(ctx, ev))
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), sent...)
	}

	mark := len(h.events())
	id := h.logTx(t, h.omer, "Jeweller BL66", 900, true)
	got := deliver(h.events()[mark:])
	assert.NotEmpty(t, got, "a shared transaction is announced and fires Dana's rule, as before")

	mark = len(h.events())
	_, err := h.mod.Execute(ctx, h.omer, "log_financial_transaction", map[string]interface{}{
		"action": "update", "transaction_id": float64(id), "amount": 950.0, "is_shared": false})
	require.NoError(t, err)
	got = deliver(h.events()[mark:])
	assert.Empty(t, got, "making it private is neither announced nor seen by Dana's rule")

	mark = len(h.events())
	_, err = h.mod.Execute(ctx, h.omer, "log_financial_transaction", map[string]interface{}{"action": "delete", "transaction_id": float64(id)})
	require.NoError(t, err)
	got = deliver(h.events()[mark:])
	assert.Empty(t, got, "nor is deleting it")
	for _, s := range got {
		assert.False(t, strings.Contains(s, "Jeweller"), fmt.Sprint(s))
	}
}
