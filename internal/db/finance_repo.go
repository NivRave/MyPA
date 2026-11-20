package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(
		&models.FinancialTransaction{},
		&models.CategoryBudget{},
		&models.RecurringBill{},
	)
}

// TransactionFilter defines filtering criteria for transaction lists.
type TransactionFilter struct {
	UserID    string
	Category  string
	Type      string // "expense", "income"
	StartDate *time.Time
	EndDate   *time.Time
	Merchant  string
	Limit     int
	Offset    int
}

// CategorySpendSummary captures actual spending and budget utilization for a category.
type CategorySpendSummary struct {
	Category          string  `json:"category"`
	ActualExpense     float64 `json:"actual_expense"`
	ActualIncome      float64 `json:"actual_income"`
	MonthlyBudget     float64 `json:"monthly_budget"`
	RemainingBudget   float64 `json:"remaining_budget"`
	PercentUsed       float64 `json:"percent_used"`
	AlertThresholdPct float64 `json:"alert_threshold_pct"`
	IsOverBudget      bool    `json:"is_over_budget"`
	IsOverThreshold   bool    `json:"is_over_threshold"`
}

// FinancialSummaryResult aggregates income, expenses, category budgets, and user distributions.
type FinancialSummaryResult struct {
	TotalExpense     float64                         `json:"total_expense"`
	TotalIncome      float64                         `json:"total_income"`
	NetSavings       float64                         `json:"net_savings"`
	CategorySummary  map[string]CategorySpendSummary `json:"category_summary"`
	UserSummary      map[string]float64              `json:"user_summary"`
	TransactionCount int                             `json:"transaction_count"`
	StartDate        time.Time                       `json:"start_date"`
	EndDate          time.Time                       `json:"end_date"`
}

// HistoricalAverageResult represents prior spending averages for anomaly comparison.
type HistoricalAverageResult struct {
	Average   float64 `json:"average"`
	Count     int64   `json:"count"`
	MatchedOn string  `json:"matched_on"` // "merchant", "category", or "none"
}

// FinanceRepository defines operations for transactions, budgets, anomaly detection, and recurring bills.
type FinanceRepository interface {
	// Every method is confined to the scope's household (V4-30, ADR 0002 Decision 3).

	// Transactions
	CreateTransaction(ctx context.Context, s identity.Scope, tx *models.FinancialTransaction) error
	GetTransaction(ctx context.Context, s identity.Scope, id uint) (*models.FinancialTransaction, error)
	ListTransactions(ctx context.Context, s identity.Scope, filter TransactionFilter) ([]models.FinancialTransaction, error)

	// Aggregations & Anomaly Detection
	GetMonthlyCategorySpend(ctx context.Context, s identity.Scope, category string, year int, month time.Month) (float64, error)
	GetMonthlyTotalsByCategory(ctx context.Context, s identity.Scope, year int, month time.Month) (map[string]float64, error)
	GetFinancialSummary(ctx context.Context, s identity.Scope, userID string, startDate, endDate time.Time) (*FinancialSummaryResult, error)
	GetHistoricalAverage(ctx context.Context, s identity.Scope, category, merchant string, excludeID uint) (*HistoricalAverageResult, error)

	// Category Budgets
	SetCategoryBudget(ctx context.Context, s identity.Scope, budget *models.CategoryBudget) error
	GetCategoryBudget(ctx context.Context, s identity.Scope, category string) (*models.CategoryBudget, error)
	ListCategoryBudgets(ctx context.Context, s identity.Scope) ([]models.CategoryBudget, error)
	DeleteCategoryBudget(ctx context.Context, s identity.Scope, id uint) error

	// Recurring Bills
	CreateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error
	UpdateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error
	GetRecurringBill(ctx context.Context, s identity.Scope, id uint) (*models.RecurringBill, error)
	ListRecurringBills(ctx context.Context, s identity.Scope) ([]models.RecurringBill, error)
	DeleteRecurringBill(ctx context.Context, s identity.Scope, id uint) error
}

// FinanceRepo is the GORM-backed implementation of FinanceRepository.
type FinanceRepo struct {
	db *gorm.DB
}

// NewFinanceRepository creates a new GORM-backed FinanceRepository.
func NewFinanceRepository(db *gorm.DB) FinanceRepository {
	return &FinanceRepo{db: db}
}

// scoped starts a query confined to the scope's household; an invalid scope is refused.
func (r *FinanceRepo) scoped(ctx context.Context, s identity.Scope) (*gorm.DB, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	return r.db.WithContext(ctx).Where("household_id = ?", s.HouseholdID()), nil
}

// CreateTransaction inserts a new financial transaction into the database.
func (r *FinanceRepo) CreateTransaction(ctx context.Context, s identity.Scope, tx *models.FinancialTransaction) error {
	if tx == nil {
		return errors.New("transaction cannot be nil")
	}
	if err := stamp(s, &tx.HouseholdID, &tx.FamilyGroup); err != nil {
		return err
	}
	if tx.TransactionDate.IsZero() {
		tx.TransactionDate = time.Now()
	}
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = time.Now()
	}
	tx.Category = models.NormalizeFinanceCategory(tx.Category)
	tx.Type = models.NormalizeTransactionType(tx.Type)
	if strings.TrimSpace(tx.Currency) == "" {
		tx.Currency = s.Currency("USD")
	}
	if strings.TrimSpace(tx.Source) == "" {
		tx.Source = models.SourceChat
	}

	result := r.db.WithContext(ctx).Create(tx)
	if result.Error != nil {
		return fmt.Errorf("failed to create transaction: %w", result.Error)
	}
	return nil
}

// GetTransaction retrieves a single transaction by ID.
func (r *FinanceRepo) GetTransaction(ctx context.Context, s identity.Scope, id uint) (*models.FinancialTransaction, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	var tx models.FinancialTransaction
	result := q.Where("id = ?", id).First(&tx)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get transaction %d: %w", id, result.Error)
	}
	return &tx, nil
}

// ListTransactions returns filtered transactions of the scope's household.
func (r *FinanceRepo) ListTransactions(ctx context.Context, s identity.Scope, filter TransactionFilter) ([]models.FinancialTransaction, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	q = q.Model(&models.FinancialTransaction{})

	if filter.UserID != "" {
		where, args := personIs("user_id", "person_id", filter.UserID)
		q = q.Where(where, args...)
	}
	if filter.Category != "" {
		q = q.Where("category = ?", models.NormalizeFinanceCategory(filter.Category))
	}
	if filter.Type != "" {
		q = q.Where("type = ?", models.NormalizeTransactionType(filter.Type))
	}
	if filter.StartDate != nil {
		q = q.Where("transaction_date >= ?", *filter.StartDate)
	}
	if filter.EndDate != nil {
		q = q.Where("transaction_date < ?", *filter.EndDate)
	}
	if filter.Merchant != "" {
		q = q.Where("LOWER(merchant) LIKE ?", "%"+strings.ToLower(strings.TrimSpace(filter.Merchant))+"%")
	}

	q = q.Order("transaction_date DESC, id DESC")
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}

	var txs []models.FinancialTransaction
	if err := q.Find(&txs).Error; err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}
	return txs, nil
}

// GetMonthlyCategorySpend computes total expenses for a category in a given month.
func (r *FinanceRepo) GetMonthlyCategorySpend(ctx context.Context, s identity.Scope, category string, year int, month time.Month) (float64, error) {
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	q, err := r.scoped(ctx, s)
	if err != nil {
		return 0, err
	}

	var total float64
	row := q.Model(&models.FinancialTransaction{}).
		Where("type = ? AND category = ? AND transaction_date >= ? AND transaction_date < ?",
			models.TransactionTypeExpense, models.NormalizeFinanceCategory(category), start, end).
		Select("COALESCE(SUM(amount), 0)").Row()

	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("failed to compute monthly category spend: %w", err)
	}
	return total, nil
}

// GetMonthlyTotalsByCategory computes total expense spend per category for the specified month.
func (r *FinanceRepo) GetMonthlyTotalsByCategory(ctx context.Context, s identity.Scope, year int, month time.Month) (map[string]float64, error) {
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}

	type catSum struct {
		Category string
		Total    float64
	}
	var sums []catSum
	err = q.Model(&models.FinancialTransaction{}).
		Where("type = ? AND transaction_date >= ? AND transaction_date < ?",
			models.TransactionTypeExpense, start, end).
		Select("category, COALESCE(SUM(amount), 0) as total").
		Group("category").Scan(&sums).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query monthly totals by category: %w", err)
	}

	out := make(map[string]float64)
	for _, s := range sums {
		out[s.Category] = s.Total
	}
	return out, nil
}

// GetHistoricalAverage calculates historical average expense for anomaly detection.
// It prioritizes exact merchant matches first; if no records exist for that merchant,
// it falls back to the historical category average.
func (r *FinanceRepo) GetHistoricalAverage(ctx context.Context, s identity.Scope, category, merchant string, excludeID uint) (*HistoricalAverageResult, error) {
	cleanMerchant := strings.TrimSpace(merchant)
	cleanCategory := models.NormalizeFinanceCategory(category)
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}

	// 1. Try merchant match first if merchant provided
	if cleanMerchant != "" {
		q := r.db.WithContext(ctx).Model(&models.FinancialTransaction{}).
			Where("household_id = ? AND type = ? AND LOWER(merchant) = ?",
				s.HouseholdID(), models.TransactionTypeExpense, strings.ToLower(cleanMerchant))
		if excludeID > 0 {
			q = q.Where("id != ?", excludeID)
		}

		type avgResult struct {
			Avg   float64
			Count int64
		}
		var res avgResult
		err := q.Select("COALESCE(AVG(amount), 0) as avg, COUNT(*) as count").Scan(&res).Error
		if err != nil {
			return nil, fmt.Errorf("failed to query historical merchant average: %w", err)
		}
		if res.Count > 0 {
			return &HistoricalAverageResult{
				Average:   res.Avg,
				Count:     res.Count,
				MatchedOn: "merchant",
			}, nil
		}
	}

	// 2. Fall back to category match
	if cleanCategory != "" {
		q := r.db.WithContext(ctx).Model(&models.FinancialTransaction{}).
			Where("household_id = ? AND type = ? AND category = ?",
				s.HouseholdID(), models.TransactionTypeExpense, cleanCategory)
		if excludeID > 0 {
			q = q.Where("id != ?", excludeID)
		}

		type avgResult struct {
			Avg   float64
			Count int64
		}
		var res avgResult
		err := q.Select("COALESCE(AVG(amount), 0) as avg, COUNT(*) as count").Scan(&res).Error
		if err != nil {
			return nil, fmt.Errorf("failed to query historical category average: %w", err)
		}
		if res.Count > 0 {
			return &HistoricalAverageResult{
				Average:   res.Avg,
				Count:     res.Count,
				MatchedOn: "category",
			}, nil
		}
	}

	return &HistoricalAverageResult{
		Average:   0,
		Count:     0,
		MatchedOn: "none",
	}, nil
}

// GetFinancialSummary aggregates expenses, income, category summaries, and user distributions.
func (r *FinanceRepo) GetFinancialSummary(ctx context.Context, s identity.Scope, userID string, startDate, endDate time.Time) (*FinancialSummaryResult, error) {
	if startDate.IsZero() {
		now := time.Now()
		startDate = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if endDate.IsZero() {
		endDate = startDate.AddDate(0, 1, 0)
	}

	// Query transactions
	filter := TransactionFilter{
		UserID:    userID,
		StartDate: &startDate,
		EndDate:   &endDate,
	}
	txs, err := r.ListTransactions(ctx, s, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to get transactions for summary: %w", err)
	}

	// Query budgets
	budgets, err := r.ListCategoryBudgets(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("failed to get budgets for summary: %w", err)
	}

	budgetMap := make(map[string]models.CategoryBudget)
	for _, b := range budgets {
		budgetMap[b.Category] = b
	}

	res := &FinancialSummaryResult{
		CategorySummary:  make(map[string]CategorySpendSummary),
		UserSummary:      make(map[string]float64),
		TransactionCount: len(txs),
		StartDate:        startDate,
		EndDate:          endDate,
	}

	// Initialize category summary with configured budgets
	for cat, b := range budgetMap {
		res.CategorySummary[cat] = CategorySpendSummary{
			Category:          cat,
			MonthlyBudget:     b.MonthlyLimit,
			AlertThresholdPct: b.AlertThresholdPct,
			RemainingBudget:   b.MonthlyLimit,
		}
	}

	for _, tx := range txs {
		if tx.Type == models.TransactionTypeIncome {
			res.TotalIncome += tx.Amount
			catSummary := res.CategorySummary[tx.Category]
			catSummary.ActualIncome += tx.Amount
			res.CategorySummary[tx.Category] = catSummary
		} else {
			res.TotalExpense += tx.Amount
			if tx.UserID != "" {
				res.UserSummary[tx.UserID] += tx.Amount
			}

			catSummary, exists := res.CategorySummary[tx.Category]
			if !exists {
				catSummary = CategorySpendSummary{
					Category: tx.Category,
				}
			}
			catSummary.ActualExpense += tx.Amount
			res.CategorySummary[tx.Category] = catSummary
		}
	}

	// Finalize category summaries (remaining, percent used, over budget/threshold flags)
	for cat, s := range res.CategorySummary {
		b, hasBudget := budgetMap[cat]
		if hasBudget && b.MonthlyLimit > 0 {
			s.MonthlyBudget = b.MonthlyLimit
			s.AlertThresholdPct = b.AlertThresholdPct
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

// SetCategoryBudget upserts a monthly budget limit for a category of the scope's household.
func (r *FinanceRepo) SetCategoryBudget(ctx context.Context, s identity.Scope, budget *models.CategoryBudget) error {
	if budget == nil {
		return errors.New("budget cannot be nil")
	}
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	if err := stamp(s, &budget.HouseholdID, &budget.FamilyGroup); err != nil {
		return err
	}
	budget.Category = models.NormalizeFinanceCategory(budget.Category)
	if budget.CreatedAt.IsZero() {
		budget.CreatedAt = time.Now()
	}
	budget.UpdatedAt = time.Now()
	if strings.TrimSpace(budget.Currency) == "" {
		budget.Currency = s.Currency("USD")
	}
	if budget.AlertThresholdPct <= 0 {
		budget.AlertThresholdPct = 80.0
	}

	var existing models.CategoryBudget
	err = q.Where("category = ?", budget.Category).First(&existing).Error

	if err == nil {
		budget.ID = existing.ID
		return r.db.WithContext(ctx).Model(&models.CategoryBudget{}).
			Where("household_id = ? AND id = ?", s.HouseholdID(), existing.ID).Updates(map[string]interface{}{
			"monthly_limit":       budget.MonthlyLimit,
			"currency":            budget.Currency,
			"alert_threshold_pct": budget.AlertThresholdPct,
			"updated_at":          budget.UpdatedAt,
		}).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(budget).Error
	}
	return fmt.Errorf("failed to check existing category budget: %w", err)
}

// GetCategoryBudget retrieves a category budget by family group and category name.
func (r *FinanceRepo) GetCategoryBudget(ctx context.Context, s identity.Scope, category string) (*models.CategoryBudget, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	var budget models.CategoryBudget
	err = q.Where("category = ?", models.NormalizeFinanceCategory(category)).First(&budget).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get category budget: %w", err)
	}
	return &budget, nil
}

// ListCategoryBudgets returns all configured category budgets of the scope's household.
func (r *FinanceRepo) ListCategoryBudgets(ctx context.Context, s identity.Scope) ([]models.CategoryBudget, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	var budgets []models.CategoryBudget
	err = q.Order("category ASC").
		Find(&budgets).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list category budgets: %w", err)
	}
	return budgets, nil
}

// DeleteCategoryBudget deletes a category budget by ID.
func (r *FinanceRepo) DeleteCategoryBudget(ctx context.Context, s identity.Scope, id uint) error {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	result := q.Where("id = ?", id).Delete(&models.CategoryBudget{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete category budget %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("category budget %d not found", id)
	}
	return nil
}

// CreateRecurringBill creates a new recurring bill.
func (r *FinanceRepo) CreateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	if bill == nil {
		return errors.New("recurring bill cannot be nil")
	}
	if err := stamp(s, &bill.HouseholdID, &bill.FamilyGroup); err != nil {
		return err
	}
	if bill.CreatedAt.IsZero() {
		bill.CreatedAt = time.Now()
	}
	if bill.UpdatedAt.IsZero() {
		bill.UpdatedAt = time.Now()
	}
	if strings.TrimSpace(bill.Currency) == "" {
		bill.Currency = s.Currency("USD")
	}
	if strings.TrimSpace(bill.BillingCycle) == "" {
		bill.BillingCycle = models.BillingCycleMonthly
	}
	bill.Category = models.NormalizeFinanceCategory(bill.Category)

	result := r.db.WithContext(ctx).Create(bill)
	if result.Error != nil {
		return fmt.Errorf("failed to create recurring bill: %w", result.Error)
	}
	return nil
}

// UpdateRecurringBill updates an existing recurring bill.
func (r *FinanceRepo) UpdateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	if bill == nil || bill.ID == 0 {
		return errors.New("valid recurring bill with ID is required")
	}
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	bill.UpdatedAt = time.Now()
	bill.Category = models.NormalizeFinanceCategory(bill.Category)

	result := q.Model(&models.RecurringBill{}).Where("id = ?", bill.ID).Updates(map[string]interface{}{
		"name":            bill.Name,
		"category":        bill.Category,
		"expected_amount": bill.ExpectedAmount,
		"currency":        bill.Currency,
		"billing_cycle":   bill.BillingCycle,
		"next_due_day":    bill.NextDueDay,
		"auto_renew":      bill.AutoRenew,
		"updated_at":      bill.UpdatedAt,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update recurring bill %d: %w", bill.ID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("recurring bill %d not found", bill.ID)
	}
	return nil
}

// GetRecurringBill retrieves a recurring bill by ID.
func (r *FinanceRepo) GetRecurringBill(ctx context.Context, s identity.Scope, id uint) (*models.RecurringBill, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	var bill models.RecurringBill
	result := q.Where("id = ?", id).First(&bill)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get recurring bill %d: %w", id, result.Error)
	}
	return &bill, nil
}

// ListRecurringBills returns all recurring bills of the scope's household.
func (r *FinanceRepo) ListRecurringBills(ctx context.Context, s identity.Scope) ([]models.RecurringBill, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	var bills []models.RecurringBill
	err = q.Order("next_due_day ASC, name ASC").
		Find(&bills).Error

	if err != nil {
		return nil, fmt.Errorf("failed to list recurring bills: %w", err)
	}
	return bills, nil
}

// DeleteRecurringBill deletes a recurring bill by ID.
func (r *FinanceRepo) DeleteRecurringBill(ctx context.Context, s identity.Scope, id uint) error {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	result := q.Where("id = ?", id).Delete(&models.RecurringBill{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete recurring bill %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("recurring bill %d not found", id)
	}
	return nil
}

// Ensure *Client implements FinanceRepository
var _ FinanceRepository = (*Client)(nil)

func (c *Client) financeRepo() *FinanceRepo {
	return &FinanceRepo{db: c.DB}
}

func (c *Client) CreateTransaction(ctx context.Context, s identity.Scope, tx *models.FinancialTransaction) error {
	return c.financeRepo().CreateTransaction(ctx, s, tx)
}

func (c *Client) GetTransaction(ctx context.Context, s identity.Scope, id uint) (*models.FinancialTransaction, error) {
	return c.financeRepo().GetTransaction(ctx, s, id)
}

func (c *Client) ListTransactions(ctx context.Context, s identity.Scope, filter TransactionFilter) ([]models.FinancialTransaction, error) {
	return c.financeRepo().ListTransactions(ctx, s, filter)
}

func (c *Client) GetMonthlyCategorySpend(ctx context.Context, s identity.Scope, category string, year int, month time.Month) (float64, error) {
	return c.financeRepo().GetMonthlyCategorySpend(ctx, s, category, year, month)
}

func (c *Client) GetMonthlyTotalsByCategory(ctx context.Context, s identity.Scope, year int, month time.Month) (map[string]float64, error) {
	return c.financeRepo().GetMonthlyTotalsByCategory(ctx, s, year, month)
}

func (c *Client) GetFinancialSummary(ctx context.Context, s identity.Scope, userID string, startDate, endDate time.Time) (*FinancialSummaryResult, error) {
	return c.financeRepo().GetFinancialSummary(ctx, s, userID, startDate, endDate)
}

func (c *Client) GetHistoricalAverage(ctx context.Context, s identity.Scope, category, merchant string, excludeID uint) (*HistoricalAverageResult, error) {
	return c.financeRepo().GetHistoricalAverage(ctx, s, category, merchant, excludeID)
}

func (c *Client) SetCategoryBudget(ctx context.Context, s identity.Scope, budget *models.CategoryBudget) error {
	return c.financeRepo().SetCategoryBudget(ctx, s, budget)
}

func (c *Client) GetCategoryBudget(ctx context.Context, s identity.Scope, category string) (*models.CategoryBudget, error) {
	return c.financeRepo().GetCategoryBudget(ctx, s, category)
}

func (c *Client) ListCategoryBudgets(ctx context.Context, s identity.Scope) ([]models.CategoryBudget, error) {
	return c.financeRepo().ListCategoryBudgets(ctx, s)
}

func (c *Client) DeleteCategoryBudget(ctx context.Context, s identity.Scope, id uint) error {
	return c.financeRepo().DeleteCategoryBudget(ctx, s, id)
}

func (c *Client) CreateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	return c.financeRepo().CreateRecurringBill(ctx, s, bill)
}

func (c *Client) UpdateRecurringBill(ctx context.Context, s identity.Scope, bill *models.RecurringBill) error {
	return c.financeRepo().UpdateRecurringBill(ctx, s, bill)
}

func (c *Client) GetRecurringBill(ctx context.Context, s identity.Scope, id uint) (*models.RecurringBill, error) {
	return c.financeRepo().GetRecurringBill(ctx, s, id)
}

func (c *Client) ListRecurringBills(ctx context.Context, s identity.Scope) ([]models.RecurringBill, error) {
	return c.financeRepo().ListRecurringBills(ctx, s)
}

func (c *Client) DeleteRecurringBill(ctx context.Context, s identity.Scope, id uint) error {
	return c.financeRepo().DeleteRecurringBill(ctx, s, id)
}
