package finance

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryFinanceRepo implements db.FinanceRepository for unit testing.
type inMemoryFinanceRepo struct {
	mu           sync.RWMutex
	transactions map[uint]*models.FinancialTransaction
	budgets      map[string]*models.CategoryBudget // key: householdID + ":" + category
	bills        map[uint]*models.RecurringBill
	nextTxID     uint
	nextBudgetID uint
	nextBillID   uint
}

func newInMemoryFinanceRepo() *inMemoryFinanceRepo {
	return &inMemoryFinanceRepo{
		transactions: make(map[uint]*models.FinancialTransaction),
		budgets:      make(map[string]*models.CategoryBudget),
		bills:        make(map[uint]*models.RecurringBill),
		nextTxID:     1,
		nextBudgetID: 1,
		nextBillID:   1,
	}
}

func (r *inMemoryFinanceRepo) CreateTransaction(ctx context.Context, s identity.Scope, tx *models.FinancialTransaction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx.HouseholdID, tx.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
	tx.ID = r.nextTxID
	r.nextTxID++
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = time.Now()
	}
	if tx.TransactionDate.IsZero() {
		tx.TransactionDate = time.Now()
	}
	tx.Category = models.NormalizeFinanceCategory(tx.Category)
	tx.Type = models.NormalizeTransactionType(tx.Type)
	cp := *tx
	r.transactions[tx.ID] = &cp
	return nil
}

func (r *inMemoryFinanceRepo) GetTransaction(ctx context.Context, s identity.Scope, id uint) (*models.FinancialTransaction, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tx, exists := r.transactions[id]
	if !exists || tx.HouseholdID != s.HouseholdID() {
		return nil, nil
	}
	cp := *tx
	return &cp, nil
}

func (r *inMemoryFinanceRepo) ListTransactions(ctx context.Context, s identity.Scope, filter db.TransactionFilter) ([]models.FinancialTransaction, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var res []models.FinancialTransaction
	for _, tx := range r.transactions {
		if tx.HouseholdID != s.HouseholdID() {
			continue
		}
		if filter.UserID != "" && tx.UserID != filter.UserID {
			continue
		}
		if filter.Category != "" && tx.Category != models.NormalizeFinanceCategory(filter.Category) {
			continue
		}
		if filter.Type != "" && tx.Type != models.NormalizeTransactionType(filter.Type) {
			continue
		}
		if filter.StartDate != nil && tx.TransactionDate.Before(*filter.StartDate) {
			continue
		}
		if filter.EndDate != nil && !tx.TransactionDate.Before(*filter.EndDate) {
			continue
		}
		if filter.Merchant != "" && !strings.Contains(strings.ToLower(tx.Merchant), strings.ToLower(filter.Merchant)) {
			continue
		}
		res = append(res, *tx)
	}
	return res, nil
}

func (r *inMemoryFinanceRepo) GetMonthlyCategorySpend(ctx context.Context, s identity.Scope, category string, year int, month time.Month) (float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normCat := models.NormalizeFinanceCategory(category)
	var total float64
	for _, tx := range r.transactions {
		if tx.HouseholdID == s.HouseholdID() &&
			tx.Type == models.TransactionTypeExpense &&
			tx.Category == normCat &&
			tx.TransactionDate.Year() == year &&
			tx.TransactionDate.Month() == month {
			total += tx.Amount
		}
	}
	return total, nil
}

func (r *inMemoryFinanceRepo) GetMonthlyTotalsByCategory(ctx context.Context, s identity.Scope, year int, month time.Month) (map[string]float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	totals := make(map[string]float64)
	for _, tx := range r.transactions {
		if tx.HouseholdID == s.HouseholdID() &&
			tx.Type == models.TransactionTypeExpense &&
			tx.TransactionDate.Year() == year &&
			tx.TransactionDate.Month() == month {
			totals[tx.Category] += tx.Amount
		}
	}
	return totals, nil
}

func (r *inMemoryFinanceRepo) GetHistoricalAverage(ctx context.Context, s identity.Scope, category, merchant string, excludeID uint) (*db.HistoricalAverageResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cleanMerchant := strings.TrimSpace(strings.ToLower(merchant))
	cleanCategory := models.NormalizeFinanceCategory(category)

	// 1. Merchant match first
	if cleanMerchant != "" {
		var sum float64
		var count int64
		for _, tx := range r.transactions {
			if tx.HouseholdID == s.HouseholdID() &&
				tx.Type == models.TransactionTypeExpense &&
				strings.ToLower(strings.TrimSpace(tx.Merchant)) == cleanMerchant &&
				tx.ID != excludeID {
				sum += tx.Amount
				count++
			}
		}
		if count > 0 {
			return &db.HistoricalAverageResult{
				Average:   sum / float64(count),
				Count:     count,
				MatchedOn: "merchant",
			}, nil
		}
	}

	// 2. Category match fallback
	if cleanCategory != "" {
		var sum float64
		var count int64
		for _, tx := range r.transactions {
			if tx.HouseholdID == s.HouseholdID() &&
				tx.Type == models.TransactionTypeExpense &&
				tx.Category == cleanCategory &&
				tx.ID != excludeID {
				sum += tx.Amount
				count++
			}
		}
		if count > 0 {
			return &db.HistoricalAverageResult{
				Average:   sum / float64(count),
				Count:     count,
				MatchedOn: "category",
			}, nil
		}
	}

	return &db.HistoricalAverageResult{
		Average:   0,
		Count:     0,
		MatchedOn: "none",
	}, nil
}

func (r *inMemoryFinanceRepo) GetFinancialSummary(ctx context.Context, s identity.Scope, userID string, startDate, endDate time.Time) (*db.FinancialSummaryResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := &db.FinancialSummaryResult{
		CategorySummary: make(map[string]db.CategorySpendSummary),
		UserSummary:     make(map[string]float64),
		StartDate:       startDate,
		EndDate:         endDate,
	}

	// Init from configured budgets
	for _, b := range r.budgets {
		if b.HouseholdID == s.HouseholdID() {
			res.CategorySummary[b.Category] = db.CategorySpendSummary{
				Category:          b.Category,
				MonthlyBudget:     b.MonthlyLimit,
				AlertThresholdPct: b.AlertThresholdPct,
				RemainingBudget:   b.MonthlyLimit,
			}
		}
	}

	for _, tx := range r.transactions {
		if tx.HouseholdID != s.HouseholdID() {
			continue
		}
		if userID != "" && tx.UserID != userID {
			continue
		}
		if !startDate.IsZero() && tx.TransactionDate.Before(startDate) {
			continue
		}
		if !endDate.IsZero() && !tx.TransactionDate.Before(endDate) {
			continue
		}

		res.TransactionCount++
		if tx.Type == models.TransactionTypeIncome {
			res.TotalIncome += tx.Amount
			cat := res.CategorySummary[tx.Category]
			cat.Category = tx.Category
			cat.ActualIncome += tx.Amount
			res.CategorySummary[tx.Category] = cat
		} else {
			res.TotalExpense += tx.Amount
			if tx.UserID != "" {
				res.UserSummary[tx.UserID] += tx.Amount
			}
			cat := res.CategorySummary[tx.Category]
			cat.Category = tx.Category
			cat.ActualExpense += tx.Amount
			res.CategorySummary[tx.Category] = cat
		}
	}

	householdID := s.HouseholdID()
	for cat, s := range res.CategorySummary {
		key := householdID + ":" + cat
		if b, ok := r.budgets[key]; ok && b.MonthlyLimit > 0 {
			s.MonthlyBudget = b.MonthlyLimit
			s.RemainingBudget = b.MonthlyLimit - s.ActualExpense
			s.PercentUsed = (s.ActualExpense / b.MonthlyLimit) * 100.0
			s.IsOverBudget = s.ActualExpense > b.MonthlyLimit
			s.IsOverThreshold = s.ActualExpense >= (b.MonthlyLimit * b.ThresholdRatio())
		} else {
			s.RemainingBudget = -s.ActualExpense
		}
		res.CategorySummary[cat] = s
	}

	res.NetSavings = res.TotalIncome - res.TotalExpense
	return res, nil
}

func (r *inMemoryFinanceRepo) SetCategoryBudget(ctx context.Context, s identity.Scope, budget *models.CategoryBudget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	budget.HouseholdID, budget.FamilyGroup = s.HouseholdID(), s.LegacyLabel()

	normCat := models.NormalizeFinanceCategory(budget.Category)
	key := budget.HouseholdID + ":" + normCat

	if existing, exists := r.budgets[key]; exists {
		existing.MonthlyLimit = budget.MonthlyLimit
		existing.Currency = budget.Currency
		existing.AlertThresholdPct = budget.AlertThresholdPct
		existing.UpdatedAt = time.Now()
		budget.ID = existing.ID
	} else {
		budget.ID = r.nextBudgetID
		r.nextBudgetID++
		budget.Category = normCat
		budget.CreatedAt = time.Now()
		budget.UpdatedAt = time.Now()
		cp := *budget
		r.budgets[key] = &cp
	}
	return nil
}

func (r *inMemoryFinanceRepo) GetCategoryBudget(ctx context.Context, s identity.Scope, category string) (*models.CategoryBudget, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normCat := models.NormalizeFinanceCategory(category)
	key := s.HouseholdID() + ":" + normCat
	b, ok := r.budgets[key]
	if !ok {
		return nil, nil
	}
	cp := *b
	return &cp, nil
}

func (r *inMemoryFinanceRepo) ListCategoryBudgets(ctx context.Context, s identity.Scope) ([]models.CategoryBudget, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var res []models.CategoryBudget
	for _, b := range r.budgets {
		if b.HouseholdID == s.HouseholdID() {
			res = append(res, *b)
		}
	}
	return res, nil
}

func (r *inMemoryFinanceRepo) DeleteCategoryBudget(ctx context.Context, s identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for k, b := range r.budgets {
		if b.ID == id && b.HouseholdID == s.HouseholdID() {
			delete(r.budgets, k)
			return nil
		}
	}
	return nil
}

func (r *inMemoryFinanceRepo) CreateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bill.HouseholdID, bill.FamilyGroup = s.HouseholdID(), s.LegacyLabel()

	bill.ID = r.nextBillID
	r.nextBillID++
	bill.CreatedAt = time.Now()
	bill.UpdatedAt = time.Now()
	bill.Category = models.NormalizeFinanceCategory(bill.Category)
	cp := *bill
	r.bills[bill.ID] = &cp
	return nil
}

func (r *inMemoryFinanceRepo) UpdateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.bills[bill.ID]
	if !ok || existing.HouseholdID != s.HouseholdID() {
		return fmt.Errorf("bill %d not found", bill.ID)
	}
	existing.Name = bill.Name
	existing.Category = models.NormalizeFinanceCategory(bill.Category)
	existing.ExpectedAmount = bill.ExpectedAmount
	existing.Currency = bill.Currency
	existing.BillingCycle = bill.BillingCycle
	existing.NextDueDay = bill.NextDueDay
	existing.AutoRenew = bill.AutoRenew
	existing.UpdatedAt = time.Now()
	return nil
}

func (r *inMemoryFinanceRepo) GetRecurringBill(ctx context.Context, s identity.Scope, id uint) (*models.RecurringBill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.bills[id]
	if !ok || b.HouseholdID != s.HouseholdID() {
		return nil, nil
	}
	cp := *b
	return &cp, nil
}

func (r *inMemoryFinanceRepo) ListRecurringBills(ctx context.Context, s identity.Scope) ([]models.RecurringBill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var res []models.RecurringBill
	for _, b := range r.bills {
		if b.HouseholdID == s.HouseholdID() {
			res = append(res, *b)
		}
	}
	return res, nil
}

func (r *inMemoryFinanceRepo) DeleteRecurringBill(ctx context.Context, s identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if b, ok := r.bills[id]; ok && b.HouseholdID == s.HouseholdID() {
		delete(r.bills, id)
	}
	return nil
}

func makeTestCallCtx(t testing.TB, familyGroup, userID string, eventBus *events.EventBus) orchestrator.CallContext {
	t.Helper()
	return orchestrator.CallContext{
		User: &models.User{
			ID:          userID,
			Name:        "Test User",
			FamilyGroup: familyGroup,
		},
		Scope:    identitytest.Scope(t, familyGroup, userID, "family"),
		EventBus: eventBus,
		Extra:    make(map[string]interface{}),
	}
}

// ---------------------- Tests ----------------------

func TestFinanceModule_NameAndDeclarations(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)

	assert.Equal(t, "finance", mod.Name())

	decls := mod.Declarations()
	require.Len(t, decls, 4)

	toolNames := make(map[string]bool)
	for _, d := range decls {
		toolNames[d.Name] = true
	}

	assert.True(t, toolNames["log_financial_transaction"])
	assert.True(t, toolNames["get_financial_summary"])
	assert.True(t, toolNames["set_category_budget"])
	assert.True(t, toolNames["manage_recurring_bills"])
}

func TestFinanceModule_LogTransaction_Basic(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Log an expense
	out, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   75.50,
		"category": "groceries",
		"merchant": "Trader Joe's",
		"notes":    "Weekly groceries",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Transaction logged successfully")
	assert.Contains(t, out, "USD 75.50")
	assert.Contains(t, out, "groceries")
	assert.Contains(t, out, "Trader Joe's")

	// Verify in repository
	tx, err := repo.GetTransaction(ctx, callCtx.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, 75.50, tx.Amount)
	assert.Equal(t, "groceries", tx.Category)
	assert.Equal(t, "Trader Joe's", tx.Merchant)
	assert.Equal(t, "SmithFamily", tx.FamilyGroup)
	assert.Equal(t, "user-1", tx.UserID)

	// Log income
	outInc, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   3500.00,
		"type":     "income",
		"category": "other",
		"merchant": "TechCorp Payroll",
		"notes":    "Monthly salary",
	})
	require.NoError(t, err)
	assert.Contains(t, outInc, "Transaction logged successfully")
	assert.Contains(t, outInc, "Income")
}

func TestFinanceModule_LogTransaction_BudgetThresholdAlert(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Configure monthly budget for dining: $500, alert threshold at 80% ($400)
	_, err := mod.Execute(ctx, callCtx, "set_category_budget", map[string]interface{}{
		"category":            "dining",
		"monthly_limit":       500.0,
		"alert_threshold_pct": 80.0,
	})
	require.NoError(t, err)

	// 1. Spend $250 -> 50%, no warning
	out1, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   250.0,
		"category": "dining",
		"merchant": "Bistro A",
	})
	require.NoError(t, err)
	assert.NotContains(t, out1, "BUDGET ALERT")
	assert.NotContains(t, out1, "BUDGET EXCEEDED")

	// 2. Spend $160 more (total $410 -> 82% >= 80% threshold) -> should trigger BUDGET ALERT
	out2, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   160.0,
		"category": "dining",
		"merchant": "Cafe B",
	})
	require.NoError(t, err)
	assert.Contains(t, out2, "BUDGET ALERT")
	assert.Contains(t, out2, "82.0%")
	assert.NotContains(t, out2, "BUDGET EXCEEDED")

	// 3. Spend $100 more (total $510 -> 102% > 100%) -> should trigger BUDGET EXCEEDED
	out3, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   100.0,
		"category": "dining",
		"merchant": "Restaurant C",
	})
	require.NoError(t, err)
	assert.Contains(t, out3, "BUDGET EXCEEDED")
	assert.Contains(t, out3, "102.0% used")
}

func TestFinanceModule_LogTransaction_AnomalyDetection(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Seed 3 historical electricity utility bills: $100, $100, $100 (avg = $100)
	now := time.Now()
	for i := 1; i <= 3; i++ {
		require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
			FamilyGroup:     "SmithFamily",
			UserID:          "user-1",
			Type:            models.TransactionTypeExpense,
			Category:        "utilities",
			Merchant:        "City Power",
			Amount:          100.0,
			Currency:        "USD",
			TransactionDate: now.AddDate(0, -i, 0),
		}))
	}

	// 1. Log a new bill of $115 (15% increase, <= 30% anomaly threshold) -> NO anomaly warning
	outNormal, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   115.0,
		"category": "utilities",
		"merchant": "City Power",
	})
	require.NoError(t, err)
	assert.NotContains(t, outNormal, "ANOMALY DETECTED")

	// 2. Log a new bill of $145 (39.8% higher than historical avg $103.75 [from 3x$100 + 1x$115] > 30%) -> triggers ANOMALY DETECTED
	outAnomaly, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   145.0,
		"category": "utilities",
		"merchant": "City Power",
	})
	require.NoError(t, err)
	assert.Contains(t, outAnomaly, "ANOMALY DETECTED")
	assert.Contains(t, outAnomaly, "39.8% higher")
	assert.Contains(t, outAnomaly, "historical average")

	// 3. Category fallback anomaly test:
	// Seed groceries transactions with no merchant match:
	require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
		FamilyGroup:     "SmithFamily",
		Type:            models.TransactionTypeExpense,
		Category:        "groceries",
		Merchant:        "Store X",
		Amount:          50.0,
		Currency:        "USD",
		TransactionDate: now.AddDate(0, 0, -5),
	}))
	// Log new grocery from unknown merchant for $80 (60% higher than category avg $50 > 30%)
	outCatAnomaly, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   80.0,
		"category": "groceries",
		"merchant": "Brand New Organic Market",
	})
	require.NoError(t, err)
	assert.Contains(t, outCatAnomaly, "ANOMALY DETECTED")
	assert.Contains(t, outCatAnomaly, "category transactions")
}

func TestFinanceModule_GetFinancialSummary(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Set a budget
	_, err := mod.Execute(ctx, callCtx, "set_category_budget", map[string]interface{}{
		"category":      "groceries",
		"monthly_limit": 600.0,
	})
	require.NoError(t, err)

	now := time.Now()
	// Add income
	require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
		FamilyGroup:     "SmithFamily",
		UserID:          "user-1",
		Type:            models.TransactionTypeIncome,
		Category:        "other",
		Amount:          4000.0,
		TransactionDate: now,
	}))

	// Add expenses
	require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
		FamilyGroup:     "SmithFamily",
		UserID:          "user-1",
		Type:            models.TransactionTypeExpense,
		Category:        "groceries",
		Amount:          300.0,
		TransactionDate: now,
	}))
	require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
		FamilyGroup:     "SmithFamily",
		UserID:          "user-2",
		Type:            models.TransactionTypeExpense,
		Category:        "baby",
		Amount:          150.0,
		TransactionDate: now,
	}))

	// Query summary
	out, err := mod.Execute(ctx, callCtx, "get_financial_summary", map[string]interface{}{})
	require.NoError(t, err)

	assert.Contains(t, out, "Financial Summary")
	assert.Contains(t, out, "Total Income: $4000.00")
	assert.Contains(t, out, "Total Expenses: $450.00")
	assert.Contains(t, out, "Net Savings: $3550.00")
	assert.Contains(t, out, "groceries")
	assert.Contains(t, out, "Spent $300.00 / Budget $600.00")
	assert.Contains(t, out, "User user-1")
	assert.Contains(t, out, "User user-2")
}

func TestFinanceModule_ManageRecurringBills(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// 1. Add recurring bill
	addOut, err := mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action":          "add",
		"name":            "Fiber Internet",
		"category":        "utilities",
		"expected_amount": 70.0,
		"currency":        "USD",
		"billing_cycle":   "monthly",
		"next_due_day":    15,
		"auto_renew":      true,
	})
	require.NoError(t, err)
	assert.Contains(t, addOut, "Fiber Internet")
	assert.Contains(t, addOut, "added successfully")

	// 2. List recurring bills
	listOut, err := mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action": "list",
	})
	require.NoError(t, err)
	assert.Contains(t, listOut, "Fiber Internet")
	assert.Contains(t, listOut, "due day 15")

	// 3. Update recurring bill
	updateOut, err := mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action":          "update",
		"bill_id":         1,
		"expected_amount": 75.0,
		"next_due_day":    18,
	})
	require.NoError(t, err)
	assert.Contains(t, updateOut, "successfully updated")

	// Verify update in repo
	bill, err := repo.GetRecurringBill(ctx, callCtx.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, bill)
	assert.Equal(t, 75.0, bill.ExpectedAmount)
	assert.Equal(t, 18, bill.NextDueDay)

	// 4. Delete recurring bill
	delOut, err := mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action":  "delete",
		"bill_id": 1,
	})
	require.NoError(t, err)
	assert.Contains(t, delOut, "deleted")

	// Verify deletion
	billAfter, err := repo.GetRecurringBill(ctx, callCtx.Scope, 1)
	require.NoError(t, err)
	assert.Nil(t, billAfter)
}

func TestFinanceModule_MutationEvents(t *testing.T) {
	bus := events.NewEventBus()
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo, WithEventBus(bus))

	var eventsMu sync.Mutex
	var capturedEvents []events.MutationEvent

	bus.SubscribeTopic("finance", func(ctx context.Context, ev events.MutationEvent) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		capturedEvents = append(capturedEvents, ev)
		return nil
	})

	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", bus)

	// 1. Transaction logged
	_, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   45.0,
		"category": "dining",
		"merchant": "Pizza Place",
	})
	require.NoError(t, err)

	// 2. Budget set
	_, err = mod.Execute(ctx, callCtx, "set_category_budget", map[string]interface{}{
		"category":      "dining",
		"monthly_limit": 400.0,
	})
	require.NoError(t, err)

	// 3. Bill added
	_, err = mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action":          "add",
		"name":            "Gym",
		"category":        "health",
		"expected_amount": 50.0,
	})
	require.NoError(t, err)

	eventsMu.Lock()
	defer eventsMu.Unlock()

	require.Len(t, capturedEvents, 3)
	assert.Equal(t, "transaction_logged", capturedEvents[0].Action)
	assert.Equal(t, "FinancialTransaction", capturedEvents[0].EntityType)
	assert.Equal(t, "SmithFamily", capturedEvents[0].FamilyGroup)

	assert.Equal(t, "budget_set", capturedEvents[1].Action)
	assert.Equal(t, "CategoryBudget", capturedEvents[1].EntityType)

	assert.Equal(t, "recurring_bill_added", capturedEvents[2].Action)
	assert.Equal(t, "RecurringBill", capturedEvents[2].EntityType)
}

func TestFinanceModule_SystemPromptSection(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Initial prompt with empty state
	emptyPrompt := mod.SystemPromptSection(ctx, callCtx)
	assert.Contains(t, emptyPrompt, "Financial Tracking & Budget Status")
	assert.Contains(t, emptyPrompt, "Ledger ready")

	// Set budget & spend
	require.NoError(t, repo.SetCategoryBudget(ctx, callCtx.Scope, &models.CategoryBudget{
		FamilyGroup:       "SmithFamily",
		Category:          "groceries",
		MonthlyLimit:      500.0,
		Currency:          "USD",
		AlertThresholdPct: 80.0,
	}))

	now := time.Now()
	require.NoError(t, repo.CreateTransaction(ctx, callCtx.Scope, &models.FinancialTransaction{
		FamilyGroup:     "SmithFamily",
		Type:            models.TransactionTypeExpense,
		Category:        "groceries",
		Amount:          450.0, // 90% >= 80% threshold
		TransactionDate: now,
	}))

	require.NoError(t, repo.CreateRecurringBill(ctx, callCtx.Scope, &models.RecurringBill{
		FamilyGroup:    "SmithFamily",
		Name:           "Daycare",
		Category:       "baby",
		ExpectedAmount: 1200.0,
		Currency:       "USD",
		BillingCycle:   "monthly",
		NextDueDay:     now.Day(), // Due today
	}))

	prompt := mod.SystemPromptSection(ctx, callCtx)
	assert.Contains(t, prompt, "groceries")
	assert.Contains(t, prompt, "Spent USD 450.00 / USD 500.00 (90.0%)")
	assert.Contains(t, prompt, "NEAR LIMIT")
	assert.Contains(t, prompt, "Daycare")
	assert.Contains(t, prompt, "DUE TODAY!")
}

func TestFinanceModule_ErrorHandling(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Negative amount
	_, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   -10.0,
		"category": "dining",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "greater than 0")

	// Missing category on set_budget
	_, err = mod.Execute(ctx, callCtx, "set_category_budget", map[string]interface{}{
		"monthly_limit": 100.0,
	})
	assert.Error(t, err)

	// Missing bill_id on update
	_, err = mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action": "update",
	})
	assert.Error(t, err)

	// Unknown tool
	_, err = mod.Execute(ctx, callCtx, "invalid_finance_tool", map[string]interface{}{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown finance tool")

	// Missing family group
	noFamCtx := orchestrator.CallContext{
		User: &models.User{ID: "user-x", FamilyGroup: ""},
	}
	res, err := mod.Execute(ctx, noFamCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   50.0,
		"category": "dining",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "requires an active family group")
}

func TestFinanceModule_FlexibleArgumentParsing(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	// Test string currency with symbols and string booleans
	out, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":           "$120.50",
		"category":         "house",
		"merchant":         "Hardware Store",
		"is_shared":        "true",
		"transaction_date": "2026-11-04 14:30:00",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "USD 120.50")
	assert.Contains(t, out, "Hardware Store")

	tx, err := repo.GetTransaction(ctx, callCtx.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, 120.50, tx.Amount)
	assert.True(t, tx.IsShared)
	assert.Equal(t, 2026, tx.TransactionDate.Year())
	assert.Equal(t, time.November, tx.TransactionDate.Month())
	assert.Equal(t, 4, tx.TransactionDate.Day())
}

// TestSEC04_CrossTenant_RecurringBills_Blocked empirically verifies that recurring bills
// cannot be updated or deleted across families, and verifies legitimate same-family operations.
func TestSEC04_CrossTenant_RecurringBills_Blocked(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()

	// 1. Family Alpha creates a recurring bill
	callCtxAlpha := makeTestCallCtx(t, "family_alpha", "user_alpha", nil)
	createResp, err := mod.Execute(ctx, callCtxAlpha, "manage_recurring_bills", map[string]interface{}{
		"action":          "add",
		"name":            "Internet Fiber",
		"category":        "utilities",
		"expected_amount": 60.00,
		"currency":        "USD",
		"billing_cycle":   "monthly",
		"next_due_day":    15,
	})
	require.NoError(t, err)
	assert.Contains(t, createResp, "Internet Fiber")

	// Verify Bill 1 is in repo
	bill1, err := repo.GetRecurringBill(ctx, callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, bill1)
	assert.Equal(t, "family_alpha", bill1.FamilyGroup)

	// 2. Family Beta attempts to UPDATE Bill 1
	callCtxBeta := makeTestCallCtx(t, "family_beta", "user_beta", nil)
	_, updateErr := mod.Execute(ctx, callCtxBeta, "manage_recurring_bills", map[string]interface{}{
		"action":          "update",
		"bill_id":         1,
		"name":            "Hacked Bill",
		"expected_amount": 999.00,
	})
	assert.Error(t, updateErr, "Family Beta must NOT be able to update Family Alpha's recurring bill")
	assert.Contains(t, updateErr.Error(), "permission denied")

	// Verify bill was not mutated
	bill1After, err := repo.GetRecurringBill(ctx, callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	assert.Equal(t, "Internet Fiber", bill1After.Name)
	assert.Equal(t, 60.00, bill1After.ExpectedAmount)

	// 3. Family Beta attempts to DELETE Bill 1
	_, deleteErr := mod.Execute(ctx, callCtxBeta, "manage_recurring_bills", map[string]interface{}{
		"action":  "delete",
		"bill_id": 1,
	})
	assert.Error(t, deleteErr, "Family Beta must NOT be able to delete Family Alpha's recurring bill")
	assert.Contains(t, deleteErr.Error(), "permission denied")

	// Verify bill still exists
	bill1StillExists, err := repo.GetRecurringBill(ctx, callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, bill1StillExists)

	// 4. Non-regression: Family Alpha updates Bill 1
	updateAlphaResp, err := mod.Execute(ctx, callCtxAlpha, "manage_recurring_bills", map[string]interface{}{
		"action":          "update",
		"bill_id":         1,
		"name":            "High-Speed Internet Fiber",
		"expected_amount": 70.00,
	})
	require.NoError(t, err, "Family Alpha must be able to update their own bill")
	assert.Contains(t, updateAlphaResp, "High-Speed Internet Fiber")

	bill1Updated, err := repo.GetRecurringBill(ctx, callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	assert.Equal(t, "High-Speed Internet Fiber", bill1Updated.Name)
	assert.Equal(t, 70.00, bill1Updated.ExpectedAmount)

	// 5. Non-regression: Family Alpha deletes Bill 1
	deleteAlphaResp, err := mod.Execute(ctx, callCtxAlpha, "manage_recurring_bills", map[string]interface{}{
		"action":  "delete",
		"bill_id": 1,
	})
	require.NoError(t, err, "Family Alpha must be able to delete their own bill")
	assert.Contains(t, deleteAlphaResp, "deleted")

	bill1Deleted, err := repo.GetRecurringBill(ctx, callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	assert.Nil(t, bill1Deleted)
}

// TestFinanceModule_DefaultCurrencyComesFromTheHousehold proves the household's currency
// (V4-30: a family-level setting on the household) is the default for transactions, budgets and
// recurring bills, and that a household without one keeps USD.
func TestFinanceModule_DefaultCurrencyComesFromTheHousehold(t *testing.T) {
	store := identitytest.NewStore()
	h := store.Household("CohenFamily")
	h.Currency = "ILS"
	store.Put(h)
	scope := store.ScopeIn(t, "CohenFamily", "user-1", "family")
	require.Equal(t, "ILS", scope.Currency("USD"))

	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := orchestrator.CallContext{
		User:  &models.User{ID: "user-1", Name: "Test User", FamilyGroup: "CohenFamily"},
		Scope: scope,
		Extra: make(map[string]interface{}),
	}

	out, err := mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   42.0,
		"category": "groceries",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "ILS 42.00")
	tx, err := repo.GetTransaction(ctx, scope, 1)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, "ILS", tx.Currency)

	out, err = mod.Execute(ctx, callCtx, "set_category_budget", map[string]interface{}{
		"category":      "groceries",
		"monthly_limit": 2000.0,
	})
	require.NoError(t, err)
	assert.Contains(t, out, "ILS 2000.00")

	out, err = mod.Execute(ctx, callCtx, "manage_recurring_bills", map[string]interface{}{
		"action":          "add",
		"name":            "Arnona",
		"expected_amount": 600.0,
	})
	require.NoError(t, err)
	assert.Contains(t, out, "ILS 600.00")

	// An explicit currency still wins.
	out, err = mod.Execute(ctx, callCtx, "log_financial_transaction", map[string]interface{}{
		"amount":   10.0,
		"category": "dining",
		"currency": "eur",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "EUR 10.00")

	// A household without a currency keeps the install default, USD.
	plain := makeTestCallCtx(t, "OtherFamily", "user-2", nil)
	out, err = mod.Execute(ctx, plain, "log_financial_transaction", map[string]interface{}{
		"amount":   10.0,
		"category": "dining",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "USD 10.00")
}

// TestFinanceModule_SummaryUserFilterStaysInTheHousehold: the summary's user_id argument names
// another person, so it is resolved inside the caller's household only (ADR 0002 Decision 3).
func TestFinanceModule_SummaryUserFilterStaysInTheHousehold(t *testing.T) {
	repo := newInMemoryFinanceRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := makeTestCallCtx(t, "SmithFamily", "user-1", nil)

	_, err := mod.Execute(ctx, callCtx, "get_financial_summary", map[string]interface{}{"user_id": "someone-else"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a member of this household")

	out, err := mod.Execute(ctx, callCtx, "get_financial_summary", map[string]interface{}{"user_id": "user-1"})
	require.NoError(t, err)
	assert.Contains(t, out, "Financial Summary")
}
