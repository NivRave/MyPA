package finance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
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
	mu   sync.RWMutex
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

// Declarations defines the Gemini tool schemas for the finance domain.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "log_financial_transaction",
			Description: "Record an expense or income transaction with automatic category budget threshold checking and >30% historical average anomaly detection.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"amount": {
						Type:        genai.TypeNumber,
						Description: "Transaction amount (e.g. 150.00).",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Expense or budget category: 'baby', 'house', 'groceries', 'utilities', 'dining', 'health', 'other'.",
					},
					"merchant": {
						Type:        genai.TypeString,
						Description: "Merchant, vendor, store, or utility provider name (e.g. 'Trader Joe\\'s', 'Electric Co', 'Daycare').",
					},
					"type": {
						Type:        genai.TypeString,
						Description: "Transaction type: 'expense' (default) or 'income'.",
					},
					"currency": {
						Type:        genai.TypeString,
						Description: "3-letter currency code (e.g. 'USD', 'ILS', 'EUR'). Defaults to USD.",
					},
					"transaction_date": {
						Type:        genai.TypeString,
						Description: "Date of transaction in YYYY-MM-DD or RFC3339 format. Defaults to current time.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Additional notes or description.",
					},
					"source": {
						Type:        genai.TypeString,
						Description: "Origin of transaction: 'chat' (default), 'receipt_photo', or 'email'.",
					},
					"is_shared": {
						Type:        genai.TypeBoolean,
						Description: "Whether this is a shared family expense. Defaults to true.",
					},
				},
				Required: []string{"amount", "category"},
			},
		},
		{
			Name:        "get_financial_summary",
			Description: "Aggregate spending and income summary by category, user, and date range against monthly budgets.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"start_date": {
						Type:        genai.TypeString,
						Description: "Start date in YYYY-MM-DD or RFC3339 format. Defaults to the 1st of the current month.",
					},
					"end_date": {
						Type:        genai.TypeString,
						Description: "End date in YYYY-MM-DD or RFC3339 format. Defaults to the end of the current month.",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Optional category filter (e.g. 'groceries', 'utilities').",
					},
					"user_id": {
						Type:        genai.TypeString,
						Description: "Optional user ID filter.",
					},
				},
			},
		},
		{
			Name:        "set_category_budget",
			Description: "Configure or update monthly budget limits and alert thresholds per category.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"category": {
						Type:        genai.TypeString,
						Description: "Category to budget (e.g. 'groceries', 'dining', 'baby', 'house', 'utilities', 'health', 'other').",
					},
					"monthly_limit": {
						Type:        genai.TypeNumber,
						Description: "Monthly spending limit amount.",
					},
					"currency": {
						Type:        genai.TypeString,
						Description: "Currency code (default 'USD').",
					},
					"alert_threshold_pct": {
						Type:        genai.TypeNumber,
						Description: "Percentage of budget at which to alert before reaching limit (e.g. 80 for 80%). Defaults to 80.",
					},
				},
				Required: []string{"category", "monthly_limit"},
			},
		},
		{
			Name:        "manage_recurring_bills",
			Description: "List, add, update, or delete recurring subscriptions and utility bills.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to perform: 'list', 'add', 'update', or 'delete'.",
					},
					"bill_id": {
						Type:        genai.TypeInteger,
						Description: "ID of the recurring bill (required for 'update' and 'delete').",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "Name of the subscription or bill (e.g. 'Electric Co', 'Netflix', 'Rent').",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Category of bill (e.g. 'utilities', 'house', 'baby', 'other').",
					},
					"expected_amount": {
						Type:        genai.TypeNumber,
						Description: "Expected amount per billing cycle.",
					},
					"currency": {
						Type:        genai.TypeString,
						Description: "Currency code (default 'USD').",
					},
					"billing_cycle": {
						Type:        genai.TypeString,
						Description: "Billing cycle: 'monthly' (default) or 'yearly'.",
					},
					"next_due_day": {
						Type:        genai.TypeInteger,
						Description: "Day of the month the bill is due (1-31).",
					},
					"auto_renew": {
						Type:        genai.TypeBoolean,
						Description: "Whether the subscription automatically renews (default true).",
					},
				},
				Required: []string{"action"},
			},
		},
	}
}

// Execute dispatches and executes the requested financial tool.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	familyGroup := m.resolveFamilyGroup(callCtx)
	if familyGroup == "" {
		return "Financial tracking requires an active family group. Please ensure your account belongs to a family group.", nil
	}

	switch toolName {
	case "log_financial_transaction":
		return m.executeLogTransaction(ctx, callCtx, repo, familyGroup, args)
	case "get_financial_summary":
		return m.executeGetSummary(ctx, callCtx, repo, familyGroup, args)
	case "set_category_budget":
		return m.executeSetBudget(ctx, callCtx, repo, familyGroup, args)
	case "manage_recurring_bills":
		return m.executeManageBills(ctx, callCtx, repo, familyGroup, args)
	default:
		return "", fmt.Errorf("unknown finance tool: %s", toolName)
	}
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

// executeLogTransaction records an expense/income and verifies anomaly and budget alerts.
func (m *Module) executeLogTransaction(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, familyGroup string, args map[string]interface{}) (string, error) {
	amount := parseFloat(args["amount"], 0)
	if amount <= 0 {
		return "", fmt.Errorf("transaction amount must be greater than 0")
	}

	category := parseString(args["category"], models.CategoryOther)
	category = models.NormalizeFinanceCategory(category)
	txType := models.NormalizeTransactionType(parseString(args["type"], models.TransactionTypeExpense))
	merchant := parseString(args["merchant"], "")
	currency := strings.ToUpper(parseString(args["currency"], "USD"))
	if currency == "" {
		currency = "USD"
	}
	notes := parseString(args["notes"], "")
	source := parseString(args["source"], models.SourceChat)
	isShared := parseBool(args["is_shared"], true)

	txDate := parseDateOrDefault(args["transaction_date"], time.Now())

	var warnings []string

	// 1. Anomaly check for expenses: check if > 30% higher than historical average
	if txType == models.TransactionTypeExpense {
		hist, err := repo.GetHistoricalAverage(ctx, familyGroup, category, merchant, 0)
		if err == nil && hist != nil && hist.Count >= 1 && hist.Average > 0 {
			anomalyThreshold := hist.Average * 1.30
			if amount > anomalyThreshold {
				pctHigher := ((amount - hist.Average) / hist.Average) * 100.0
				warnings = append(warnings, fmt.Sprintf(
					"⚠️ ANOMALY DETECTED: This bill of %s %.2f is %.1f%% higher than your historical average (%s %.2f based on %d prior %s transactions).",
					currency, amount, pctHigher, currency, hist.Average, hist.Count, hist.MatchedOn,
				))
			}
		}
	}

	// 2. Persist the transaction
	userID := ""
	if callCtx.User != nil {
		userID = callCtx.User.ID
	}

	tx := &models.FinancialTransaction{
		UserID:          userID,
		FamilyGroup:     familyGroup,
		Type:            txType,
		Category:        category,
		Merchant:        merchant,
		Amount:          amount,
		Currency:        currency,
		TransactionDate: txDate,
		IsShared:        isShared,
		Source:          source,
		Notes:           notes,
		CreatedAt:       time.Now(),
	}

	if err := repo.CreateTransaction(ctx, tx); err != nil {
		return "", fmt.Errorf("failed to save transaction: %w", err)
	}

	// 3. Category budget threshold check for expenses
	if txType == models.TransactionTypeExpense {
		monthlySpend, err := repo.GetMonthlyCategorySpend(ctx, familyGroup, category, txDate.Year(), txDate.Month())
		if err == nil {
			budget, err := repo.GetCategoryBudget(ctx, familyGroup, category)
			if err == nil && budget != nil && budget.MonthlyLimit > 0 {
				thresholdRatio := budget.ThresholdRatio()
				thresholdAmount := budget.MonthlyLimit * thresholdRatio
				pctUsed := (monthlySpend / budget.MonthlyLimit) * 100.0

				if monthlySpend > budget.MonthlyLimit {
					warnings = append(warnings, fmt.Sprintf(
						"🚨 BUDGET EXCEEDED: Category '%s' spending has reached %s %.2f, exceeding the monthly budget limit of %s %.2f (%.1f%% used).",
						category, currency, monthlySpend, currency, budget.MonthlyLimit, pctUsed,
					))
				} else if monthlySpend >= thresholdAmount {
					warnings = append(warnings, fmt.Sprintf(
						"⚠️ BUDGET ALERT: Category '%s' spending has reached %s %.2f (%.1f%% of monthly budget %s %.2f, alert threshold: %.1f%%).",
						category, currency, monthlySpend, pctUsed, currency, budget.MonthlyLimit, thresholdRatio*100.0,
					))
				}
			}
		}
	}

	// 4. Emit MutationEvent
	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      "transaction_logged",
		EntityType:  "FinancialTransaction",
		EntityID:    strconv.FormatUint(uint64(tx.ID), 10),
		ActorUserID: userID,
		FamilyGroup: familyGroup,
		Summary:     fmt.Sprintf("Logged %s %s %.2f for %s (%s)", tx.Type, tx.Currency, tx.Amount, tx.Category, tx.Merchant),
		Timestamp:   time.Now(),
	}, tx)

	// 5. Construct response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ Transaction logged successfully (ID: %d):\n", tx.ID))
	displayType := tx.Type
	if len(displayType) > 0 {
		displayType = strings.ToUpper(displayType[:1]) + displayType[1:]
	}
	sb.WriteString(fmt.Sprintf("- Type: %s\n", displayType))
	sb.WriteString(fmt.Sprintf("- Amount: %s %.2f\n", tx.Currency, tx.Amount))
	sb.WriteString(fmt.Sprintf("- Category: %s\n", tx.Category))
	if tx.Merchant != "" {
		sb.WriteString(fmt.Sprintf("- Merchant: %s\n", tx.Merchant))
	}
	sb.WriteString(fmt.Sprintf("- Date: %s\n", tx.TransactionDate.Format("2006-01-02 15:04")))
	if tx.Notes != "" {
		sb.WriteString(fmt.Sprintf("- Notes: %s\n", tx.Notes))
	}

	if len(warnings) > 0 {
		sb.WriteString("\nNotices & Alerts:\n")
		for _, w := range warnings {
			sb.WriteString(fmt.Sprintf("- %s\n", w))
		}
	}

	return sb.String(), nil
}

// executeGetSummary produces an aggregated financial summary.
func (m *Module) executeGetSummary(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, familyGroup string, args map[string]interface{}) (string, error) {
	now := time.Now()
	startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	if s := parseString(args["start_date"], ""); s != "" {
		if t, err := parseFlexDate(s); err == nil {
			startDate = t
		}
	}
	if s := parseString(args["end_date"], ""); s != "" {
		if t, err := parseFlexDate(s); err == nil {
			endDate = t
		}
	}

	userID := parseString(args["user_id"], "")
	targetCategory := models.NormalizeFinanceCategory(parseString(args["category"], ""))

	summary, err := repo.GetFinancialSummary(ctx, familyGroup, userID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("failed to generate financial summary: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### 📊 Financial Summary (%s to %s)\n",
		summary.StartDate.Format("2006-01-02"), summary.EndDate.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Family Group: %s\n", familyGroup))
	sb.WriteString(fmt.Sprintf("- Total Income: $%.2f\n", summary.TotalIncome))
	sb.WriteString(fmt.Sprintf("- Total Expenses: $%.2f\n", summary.TotalExpense))
	sb.WriteString(fmt.Sprintf("- Net Savings: $%.2f\n", summary.NetSavings))
	sb.WriteString(fmt.Sprintf("- Total Transactions: %d\n\n", summary.TransactionCount))

	// Category breakdown
	sb.WriteString("#### Category Breakdown:\n")
	var cats []string
	for c := range summary.CategorySummary {
		if targetCategory != "" && targetCategory != models.CategoryOther && c != targetCategory {
			continue
		}
		cats = append(cats, c)
	}
	sort.Strings(cats)

	if len(cats) == 0 {
		sb.WriteString("No transactions recorded for the specified criteria.\n")
	} else {
		for _, c := range cats {
			item := summary.CategorySummary[c]
			var statusTag string
			if item.IsOverBudget {
				statusTag = " [🚨 OVER BUDGET]"
			} else if item.IsOverThreshold {
				statusTag = " [⚠️ ALERT THRESHOLD REACHED]"
			}

			if item.MonthlyBudget > 0 {
				sb.WriteString(fmt.Sprintf("- **%s**: Spent $%.2f / Budget $%.2f (Remaining: $%.2f, %.1f%% used)%s\n",
					item.Category, item.ActualExpense, item.MonthlyBudget, item.RemainingBudget, item.PercentUsed, statusTag))
			} else {
				sb.WriteString(fmt.Sprintf("- **%s**: Spent $%.2f (No budget limit set)\n",
					item.Category, item.ActualExpense))
			}
			if item.ActualIncome > 0 {
				sb.WriteString(fmt.Sprintf("  • Income in %s: $%.2f\n", item.Category, item.ActualIncome))
			}
		}
	}

	// User distribution
	if len(summary.UserSummary) > 0 {
		sb.WriteString("\n#### Spending by Family Member:\n")
		for u, amt := range summary.UserSummary {
			sb.WriteString(fmt.Sprintf("- User %s: $%.2f\n", u, amt))
		}
	}

	return sb.String(), nil
}

// executeSetBudget creates or updates a category budget limit.
func (m *Module) executeSetBudget(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, familyGroup string, args map[string]interface{}) (string, error) {
	category := parseString(args["category"], "")
	if strings.TrimSpace(category) == "" {
		return "", fmt.Errorf("category is required")
	}
	category = models.NormalizeFinanceCategory(category)

	limit := parseFloat(args["monthly_limit"], 0)
	if limit <= 0 {
		return "", fmt.Errorf("monthly_limit must be greater than 0")
	}

	currency := strings.ToUpper(parseString(args["currency"], "USD"))
	if currency == "" {
		currency = "USD"
	}

	threshold := parseFloat(args["alert_threshold_pct"], 80.0)
	if threshold <= 0 {
		threshold = 80.0
	}

	budget := &models.CategoryBudget{
		FamilyGroup:       familyGroup,
		Category:          category,
		MonthlyLimit:      limit,
		Currency:          currency,
		AlertThresholdPct: threshold,
	}

	if err := repo.SetCategoryBudget(ctx, budget); err != nil {
		return "", fmt.Errorf("failed to set category budget: %w", err)
	}

	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      "budget_set",
		EntityType:  "CategoryBudget",
		EntityID:    strconv.FormatUint(uint64(budget.ID), 10),
		ActorUserID: m.resolveUserID(callCtx),
		FamilyGroup: familyGroup,
		Summary:     fmt.Sprintf("Set %s monthly budget to %s %.2f (alert at %.0f%%)", budget.Category, budget.Currency, budget.MonthlyLimit, budget.AlertThresholdPct),
		Timestamp:   time.Now(),
	}, budget)

	return fmt.Sprintf("✅ Monthly budget for '%s' successfully set to %s %.2f (alert threshold: %.1f%%).",
		budget.Category, budget.Currency, budget.MonthlyLimit, budget.AlertThresholdPct), nil
}

// executeManageBills handles recurring bill list, add, update, and delete actions.
func (m *Module) executeManageBills(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, familyGroup string, args map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(parseString(args["action"], "list")))

	switch action {
	case "list":
		bills, err := repo.ListRecurringBills(ctx, familyGroup)
		if err != nil {
			return "", fmt.Errorf("failed to list recurring bills: %w", err)
		}
		if len(bills) == 0 {
			return "No recurring bills or subscriptions configured.", nil
		}
		var sb strings.Builder
		sb.WriteString("### 🔄 Recurring Bills & Subscriptions:\n")
		for _, b := range bills {
			autoRenewStr := "auto-renews"
			if !b.AutoRenew {
				autoRenewStr = "manual renewal"
			}
			sb.WriteString(fmt.Sprintf("- **[%d] %s** (%s): %s %.2f (%s, due day %d, %s)\n",
				b.ID, b.Name, b.Category, b.Currency, b.ExpectedAmount, b.BillingCycle, b.NextDueDay, autoRenewStr))
		}
		return sb.String(), nil

	case "add":
		name := strings.TrimSpace(parseString(args["name"], ""))
		if name == "" {
			return "", fmt.Errorf("name is required to add recurring bill")
		}
		category := models.NormalizeFinanceCategory(parseString(args["category"], models.CategoryUtilities))
		amount := parseFloat(args["expected_amount"], 0)
		currency := strings.ToUpper(parseString(args["currency"], "USD"))
		if currency == "" {
			currency = "USD"
		}
		cycle := strings.ToLower(parseString(args["billing_cycle"], models.BillingCycleMonthly))
		if cycle != models.BillingCycleYearly {
			cycle = models.BillingCycleMonthly
		}
		dueDay := parseInt(args["next_due_day"], 1)
		if dueDay < 1 || dueDay > 31 {
			dueDay = 1
		}
		autoRenew := parseBool(args["auto_renew"], true)

		bill := &models.RecurringBill{
			FamilyGroup:    familyGroup,
			Name:           name,
			Category:       category,
			ExpectedAmount: amount,
			Currency:       currency,
			BillingCycle:   cycle,
			NextDueDay:     dueDay,
			AutoRenew:      autoRenew,
		}

		if err := repo.CreateRecurringBill(ctx, bill); err != nil {
			return "", fmt.Errorf("failed to create recurring bill: %w", err)
		}

		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "recurring_bill_added",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(bill.ID), 10),
			ActorUserID: m.resolveUserID(callCtx),
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Added recurring bill %s (%s %.2f)", bill.Name, bill.Currency, bill.ExpectedAmount),
			Timestamp:   time.Now(),
		}, bill)

		return fmt.Sprintf("✅ Recurring bill '%s' added successfully (ID: %d, %s %.2f, due day %d).",
			bill.Name, bill.ID, bill.Currency, bill.ExpectedAmount, bill.NextDueDay), nil

	case "update":
		id := uint(parseInt(args["bill_id"], 0))
		if id == 0 {
			return "", fmt.Errorf("bill_id is required to update a recurring bill")
		}
		existing, err := repo.GetRecurringBill(ctx, id)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve recurring bill %d: %w", id, err)
		}
		if existing == nil {
			return "", fmt.Errorf("recurring bill %d not found", id)
		}

		if n := parseString(args["name"], ""); n != "" {
			existing.Name = n
		}
		if c := parseString(args["category"], ""); c != "" {
			existing.Category = models.NormalizeFinanceCategory(c)
		}
		if a := parseFloat(args["expected_amount"], -1); a >= 0 {
			existing.ExpectedAmount = a
		}
		if cur := parseString(args["currency"], ""); cur != "" {
			existing.Currency = strings.ToUpper(cur)
		}
		if cyc := parseString(args["billing_cycle"], ""); cyc != "" {
			existing.BillingCycle = strings.ToLower(cyc)
		}
		if d := parseInt(args["next_due_day"], 0); d >= 1 && d <= 31 {
			existing.NextDueDay = d
		}
		if ar, ok := args["auto_renew"]; ok {
			existing.AutoRenew = parseBool(ar, existing.AutoRenew)
		}

		if err := repo.UpdateRecurringBill(ctx, existing); err != nil {
			return "", fmt.Errorf("failed to update recurring bill: %w", err)
		}

		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "recurring_bill_updated",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(existing.ID), 10),
			ActorUserID: m.resolveUserID(callCtx),
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Updated recurring bill %s (ID %d)", existing.Name, existing.ID),
			Timestamp:   time.Now(),
		}, existing)

		return fmt.Sprintf("✅ Recurring bill '%s' (ID: %d) successfully updated.", existing.Name, existing.ID), nil

	case "delete":
		id := uint(parseInt(args["bill_id"], 0))
		if id == 0 {
			return "", fmt.Errorf("bill_id is required to delete a recurring bill")
		}
		existing, err := repo.GetRecurringBill(ctx, id)
		if err != nil {
			return "", fmt.Errorf("failed to check recurring bill %d: %w", id, err)
		}
		if existing == nil {
			return "", fmt.Errorf("recurring bill %d not found", id)
		}

		if err := repo.DeleteRecurringBill(ctx, id); err != nil {
			return "", fmt.Errorf("failed to delete recurring bill: %w", err)
		}

		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "recurring_bill_deleted",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(id), 10),
			ActorUserID: m.resolveUserID(callCtx),
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Deleted recurring bill %s (ID %d)", existing.Name, id),
			Timestamp:   time.Now(),
		}, existing)

		return fmt.Sprintf("✅ Recurring bill '%s' (ID: %d) deleted.", existing.Name, id), nil

	default:
		return "", fmt.Errorf("unsupported action '%s', expected 'list', 'add', 'update', or 'delete'", action)
	}
}

// emitMutation dispatches a mutation event to the call context or module event bus.
func (m *Module) emitMutation(ctx context.Context, callCtx orchestrator.CallContext, event events.MutationEvent, payload interface{}) {
	bus := callCtx.EventBus
	if bus == nil {
		bus = m.bus
	}
	if bus == nil {
		return
	}

	if payload != nil {
		if bytes, err := json.Marshal(payload); err == nil {
			event.AfterJSON = string(bytes)
		}
	}
	_ = bus.Publish(ctx, event)
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

func (m *Module) resolveFamilyGroup(callCtx orchestrator.CallContext) string {
	if callCtx.User != nil && strings.TrimSpace(callCtx.User.FamilyGroup) != "" {
		return strings.TrimSpace(callCtx.User.FamilyGroup)
	}
	if fg, ok := callCtx.Extra["family_group"].(string); ok && strings.TrimSpace(fg) != "" {
		return strings.TrimSpace(fg)
	}
	return ""
}

func (m *Module) resolveUserID(callCtx orchestrator.CallContext) string {
	if callCtx.User != nil {
		return callCtx.User.ID
	}
	return ""
}

// Helpers for safe argument conversion
func parseFloat(v interface{}, defaultVal float64) float64 {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return f
		}
	case string:
		clean := strings.TrimSpace(val)
		clean = strings.TrimPrefix(clean, "$")
		clean = strings.TrimPrefix(clean, "€")
		clean = strings.TrimPrefix(clean, "₪")
		if f, err := strconv.ParseFloat(clean, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func parseInt(v interface{}, defaultVal int) int {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return i
		}
	}
	return defaultVal
}

func parseString(v interface{}, defaultVal string) string {
	if v == nil {
		return defaultVal
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf("%v", v)
}

func parseBool(v interface{}, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		lower := strings.ToLower(strings.TrimSpace(val))
		if lower == "true" || lower == "1" || lower == "yes" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "no" {
			return false
		}
	}
	return defaultVal
}

func parseDateOrDefault(v interface{}, defaultVal time.Time) time.Time {
	if v == nil {
		return defaultVal
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		if t, err := parseFlexDate(s); err == nil {
			return t
		}
	}
	return defaultVal
}

func parseFlexDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date: %s", s)
}
