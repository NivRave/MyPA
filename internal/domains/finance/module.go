package finance

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"sort"
	"strings"
	"time"
)

// Option allows configuring the finance Module.
type Option func(*Module)

// WithEventBus injects an EventBus into the finance Module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for financial tracking,
// category budget management, anomaly detection, and recurring bills.
type Module struct {
	repo db.FinanceRepository
	bus  *events.EventBus
	// concurrency managed by repository
}

// NewModule initializes a new finance ToolModule.
func NewModule(repo db.FinanceRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique identifier for the finance module.
func (m *Module) Name() string {
	return "finance"
}

// SystemPromptSection injects monthly budget status, category alert overviews,
// and upcoming recurring bills due into the system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	familyGroup := m.resolveFamilyGroup(callCtx)
	if familyGroup == "" {
		return ""
	}

	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return ""
	}

	now := time.Now()
	budgets, err := repo.ListCategoryBudgets(ctx, familyGroup)
	if err != nil {
		budgets = nil
	}

	monthlySpend, err := repo.GetMonthlyTotalsByCategory(ctx, familyGroup, now.Year(), now.Month())
	if err != nil {
		monthlySpend = make(map[string]float64)
	}

	recurringBills, err := repo.ListRecurringBills(ctx, familyGroup)
	if err != nil {
		recurringBills = nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Financial Tracking & Budget Status (%s %d)\n", now.Month().String(), now.Year()))
	sb.WriteString(fmt.Sprintf("Family Group: %s\n", familyGroup))

	if len(budgets) == 0 && len(monthlySpend) == 0 && len(recurringBills) == 0 {
		sb.WriteString("No budgets or transactions logged yet for this month. Ledger ready for expenses and recurring bills.\n")
		return sb.String()
	}

	if len(budgets) > 0 || len(monthlySpend) > 0 {
		sb.WriteString("Monthly Category Budgets & Spend:\n")
		budgetMap := make(map[string]models.CategoryBudget)
		for _, b := range budgets {
			budgetMap[b.Category] = b
		}

		// Collect all categories (both configured and those with actual spend)
		catSet := make(map[string]bool)
		for c := range budgetMap {
			catSet[c] = true
		}
		for c := range monthlySpend {
			catSet[c] = true
		}

		var sortedCats []string
		for c := range catSet {
			sortedCats = append(sortedCats, c)
		}
		sort.Strings(sortedCats)

		for _, cat := range sortedCats {
			spent := monthlySpend[cat]
			b, hasBudget := budgetMap[cat]
			if hasBudget && b.MonthlyLimit > 0 {
				ratio := spent / b.MonthlyLimit
				pct := ratio * 100.0
				var statusTag string
				if spent > b.MonthlyLimit {
					statusTag = " [🚨 OVER BUDGET]"
				} else if spent >= (b.MonthlyLimit * b.ThresholdRatio()) {
					statusTag = fmt.Sprintf(" [⚠️ NEAR LIMIT (>= %.0f%%)]", b.ThresholdRatio()*100.0)
				} else {
					statusTag = " [OK]"
				}
				sb.WriteString(fmt.Sprintf("- %s: Spent %s %.2f / %s %.2f (%.1f%%)%s\n",
					cat, b.Currency, spent, b.Currency, b.MonthlyLimit, pct, statusTag))
			} else {
				sb.WriteString(fmt.Sprintf("- %s: Spent %.2f (no budget limit set)\n", cat, spent))
			}
		}
	}

	if len(recurringBills) > 0 {
		sb.WriteString("Upcoming Recurring Bills & Subscriptions:\n")
		today := now.Day()
		for _, bill := range recurringBills {
			dueStatus := fmt.Sprintf("due day %d", bill.NextDueDay)
			if bill.NextDueDay == today {
				dueStatus = "DUE TODAY!"
			} else if bill.NextDueDay > today && bill.NextDueDay <= today+7 {
				dueStatus = fmt.Sprintf("due in %d days (day %d)", bill.NextDueDay-today, bill.NextDueDay)
			}
			sb.WriteString(fmt.Sprintf("- %s (%s): %s %.2f (%s, %s)\n",
				bill.Name, bill.Category, bill.Currency, bill.ExpectedAmount, bill.BillingCycle, dueStatus))
		}
	}

	sb.WriteString("Use log_financial_transaction, get_financial_summary, set_category_budget, and manage_recurring_bills to manage family finances.")
	return sb.String()
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.FinanceRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if r, ok := callCtx.DB.(db.FinanceRepository); ok {
		return r, nil
	}
	if client, ok := callCtx.DB.(*db.Client); ok && client != nil {
		return db.NewFinanceRepository(client.DB), nil
	}
	if extraRepo, ok := callCtx.Extra["finance_repo"].(db.FinanceRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("finance: no finance repository available in module or call context")
}
