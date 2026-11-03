package models

import (
	"strings"
	"time"
)

// Supported transaction types
const (
	TransactionTypeExpense = "expense"
	TransactionTypeIncome  = "income"
)

// Standard expense & budget categories
const (
	CategoryBaby      = "baby"
	CategoryHouse     = "house"
	CategoryGroceries = "groceries"
	CategoryUtilities = "utilities"
	CategoryDining    = "dining"
	CategoryHealth    = "health"
	CategoryOther     = "other"
)

// Supported recurring billing cycles
const (
	BillingCycleMonthly = "monthly"
	BillingCycleYearly  = "yearly"
)

// Transaction origin sources
const (
	SourceChat         = "chat"
	SourceReceiptPhoto = "receipt_photo"
	SourceEmail        = "email"
)

// FinancialTransaction represents a single monetary transaction (expense or income),
// originating from conversation, receipt photos, or automated email parsing.
type FinancialTransaction struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	UserID          string    `json:"user_id" gorm:"index"`
	FamilyGroup     string    `json:"family_group" gorm:"index"`
	Type            string    `json:"type" gorm:"index"` // "expense", "income"
	Category        string    `json:"category" gorm:"index"` // "baby", "house", "groceries", "utilities", "dining", "health", "other"
	Merchant        string    `json:"merchant" gorm:"index"`
	Amount          float64   `json:"amount"`
	Currency        string    `json:"currency"`
	TransactionDate time.Time `json:"transaction_date" gorm:"index"`
	IsShared        bool      `json:"is_shared" gorm:"default:true"`
	Source          string    `json:"source"` // "chat", "receipt_photo", "email"
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
}

// CategoryBudget represents a family group's monthly spending limit and notification
// threshold for a specific life domain or expense category.
type CategoryBudget struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	FamilyGroup       string    `json:"family_group" gorm:"index;uniqueIndex:idx_fam_cat"`
	Category          string    `json:"category" gorm:"uniqueIndex:idx_fam_cat"`
	MonthlyLimit      float64   `json:"monthly_limit"`
	Currency          string    `json:"currency"`
	AlertThresholdPct float64   `json:"alert_threshold_pct"` // e.g., 80.0 for 80% (or 0.8)
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ThresholdRatio returns the budget alert threshold normalized to a 0.0 - 1.0 fraction.
func (b *CategoryBudget) ThresholdRatio() float64 {
	if b.AlertThresholdPct <= 0 {
		return 0.8 // default 80% alert threshold
	}
	if b.AlertThresholdPct > 1.0 {
		return b.AlertThresholdPct / 100.0
	}
	return b.AlertThresholdPct
}

// RecurringBill tracks expected recurring subscriptions and utility bills,
// alerting family members before due dates and flagging renewal changes.
type RecurringBill struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	FamilyGroup    string    `json:"family_group" gorm:"index"`
	Name           string    `json:"name"`
	Category       string    `json:"category"`
	ExpectedAmount float64   `json:"expected_amount"`
	Currency       string    `json:"currency"`
	BillingCycle   string    `json:"billing_cycle"` // "monthly", "yearly"
	NextDueDay     int       `json:"next_due_day"`  // Day of month 1-31
	AutoRenew      bool      `json:"auto_renew" gorm:"default:true"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// NormalizeFinanceCategory normalizes input category strings to standard categories or preserves custom ones.
func NormalizeFinanceCategory(cat string) string {
	c := strings.ToLower(strings.TrimSpace(cat))
	switch c {
	case CategoryBaby, CategoryHouse, CategoryGroceries, CategoryUtilities, CategoryDining, CategoryHealth, CategoryOther:
		return c
	default:
		if c == "" {
			return CategoryOther
		}
		return c
	}
}

// NormalizeTransactionType normalizes input transaction type to expense or income.
func NormalizeTransactionType(t string) string {
	norm := strings.ToLower(strings.TrimSpace(t))
	if norm == TransactionTypeIncome {
		return TransactionTypeIncome
	}
	return TransactionTypeExpense
}
