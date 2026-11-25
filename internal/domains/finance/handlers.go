package finance

import (
	"context"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Execute dispatches and executes the requested financial tool.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	// V4-30: the household comes from the call's scope only, never from arguments or Extra.
	s := callCtx.Scope
	if !s.Valid() {
		return "Financial tracking requires an active family group. Please ensure your account belongs to a family group.", nil
	}

	switch toolName {
	case "log_financial_transaction":
		// BL-66: one tool logs a transaction, and changes or deletes the caller's own.
		switch strings.ToLower(strings.TrimSpace(parseString(args["action"], "log"))) {
		case "", "log":
			return m.executeLogTransaction(ctx, callCtx, repo, s, args)
		case "update":
			return m.executeUpdateTransaction(ctx, callCtx, repo, s, args)
		case "delete":
			return m.executeDeleteTransaction(ctx, callCtx, repo, s, args)
		default:
			return "", fmt.Errorf("unsupported action %q, expected 'log', 'update', or 'delete'", parseString(args["action"], ""))
		}
	case "get_financial_summary":
		return m.executeGetSummary(ctx, callCtx, repo, s, args)
	case "set_category_budget":
		return m.executeSetBudget(ctx, callCtx, repo, s, args)
	case "manage_recurring_bills":
		return m.executeManageBills(ctx, callCtx, repo, s, args)
	default:
		return "", fmt.Errorf("unknown finance tool: %s", toolName)
	}
}

// executeLogTransaction records an expense/income and verifies anomaly and budget alerts.
func (m *Module) executeLogTransaction(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
	amount := parseFloat(args["amount"], 0)
	if amount <= 0 {
		return "", fmt.Errorf("transaction amount must be greater than 0")
	}

	category := parseString(args["category"], models.CategoryOther)
	category = models.NormalizeFinanceCategory(category)
	txType := models.NormalizeTransactionType(parseString(args["type"], models.TransactionTypeExpense))
	merchant := parseString(args["merchant"], "")
	currency := strings.ToUpper(parseString(args["currency"], s.Currency("USD")))
	if currency == "" {
		currency = s.Currency("USD")
	}
	notes := parseString(args["notes"], "")
	source := parseString(args["source"], models.SourceChat)
	isShared := parseBool(args["is_shared"], true)

	txDate := parseDateOrDefault(args["transaction_date"], time.Now())

	// 1. Anomaly check for expenses: check if > 30% higher than historical average
	warnings := anomalyWarnings(ctx, repo, s, txType, category, merchant, currency, amount, 0)

	// 2. Persist the transaction
	userID := ""
	if callCtx.User != nil {
		userID = callCtx.User.ID
	}

	tx := &models.FinancialTransaction{
		UserID:          userID,
		HouseholdID:     s.HouseholdID(),
		FamilyGroup:     s.LegacyLabel(),
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

	if err := repo.CreateTransaction(ctx, s, tx); err != nil {
		return "", fmt.Errorf("failed to save transaction: %w", err)
	}

	// 3. Category budget threshold check for expenses
	warnings = append(warnings, budgetWarnings(ctx, repo, s, tx)...)

	// 4. Emit MutationEvent
	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      "create",
		Kind:        "transaction_logged",
		EntityType:  "FinancialTransaction",
		EntityID:    strconv.FormatUint(uint64(tx.ID), 10),
		ActorUserID: userID,
		HouseholdID: s.HouseholdID(),
		FamilyGroup: s.LegacyLabel(),
		Summary:     fmt.Sprintf("Logged %s %s %.2f for %s (%s)", tx.Type, tx.Currency, tx.Amount, tx.Category, tx.Merchant),
		Timestamp:   time.Now(),
	}, tx)

	// 5. Construct response
	return describeTransaction(fmt.Sprintf("✅ Transaction logged successfully (ID: %d):\n", tx.ID), tx, false, warnings), nil
}

// describeTransaction is the reply about one transaction, with its notices.
func describeTransaction(head string, tx *models.FinancialTransaction, withSharing bool, warnings []string) string {
	var sb strings.Builder
	sb.WriteString(head)
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
	if withSharing {
		if tx.IsShared {
			sb.WriteString("- Shared with the household\n")
		} else {
			sb.WriteString("- Private: only you see it\n")
		}
	}

	if len(warnings) > 0 {
		sb.WriteString("\nNotices & Alerts:\n")
		for _, w := range warnings {
			sb.WriteString(fmt.Sprintf("- %s\n", w))
		}
	}
	return sb.String()
}

// anomalyWarnings is the anomaly check of an expense: more than 30% above the historical average
// of its merchant, else of its category. excludeID leaves the transaction itself out of the
// average when it is being changed (BL-66).
func anomalyWarnings(ctx context.Context, repo db.FinanceRepository, s identity.Scope, txType, category, merchant, currency string, amount float64, excludeID uint) []string {
	if txType != models.TransactionTypeExpense {
		return nil
	}
	hist, err := repo.GetHistoricalAverage(ctx, s, category, merchant, excludeID)
	if err != nil || hist == nil || hist.Count < 1 || hist.Average <= 0 {
		return nil
	}
	if amount <= hist.Average*1.30 {
		return nil
	}
	pctHigher := ((amount - hist.Average) / hist.Average) * 100.0
	return []string{fmt.Sprintf(
		"⚠️ ANOMALY DETECTED: This bill of %s %.2f is %.1f%% higher than your historical average (%s %.2f based on %d prior %s transactions).",
		currency, amount, pctHigher, currency, hist.Average, hist.Count, hist.MatchedOn,
	)}
}

// budgetWarnings is the budget check of an expense as stored: its category's spend this month
// against the category's budget.
func budgetWarnings(ctx context.Context, repo db.FinanceRepository, s identity.Scope, tx *models.FinancialTransaction) []string {
	if tx.Type != models.TransactionTypeExpense {
		return nil
	}
	category, currency, txDate := tx.Category, tx.Currency, tx.TransactionDate
	monthlySpend, err := repo.GetMonthlyCategorySpend(ctx, s, category, txDate.Year(), txDate.Month())
	if err != nil {
		return nil
	}
	budget, err := repo.GetCategoryBudget(ctx, s, category)
	if err != nil || budget == nil || budget.MonthlyLimit <= 0 {
		return nil
	}
	thresholdRatio := budget.ThresholdRatio()
	thresholdAmount := budget.MonthlyLimit * thresholdRatio
	pctUsed := (monthlySpend / budget.MonthlyLimit) * 100.0

	if monthlySpend > budget.MonthlyLimit {
		return []string{fmt.Sprintf(
			"🚨 BUDGET EXCEEDED: Category '%s' spending has reached %s %.2f, exceeding the monthly budget limit of %s %.2f (%.1f%% used).",
			category, currency, monthlySpend, currency, budget.MonthlyLimit, pctUsed,
		)}
	}
	if monthlySpend >= thresholdAmount {
		return []string{fmt.Sprintf(
			"⚠️ BUDGET ALERT: Category '%s' spending has reached %s %.2f (%.1f%% of monthly budget %s %.2f, alert threshold: %.1f%%).",
			category, currency, monthlySpend, pctUsed, currency, budget.MonthlyLimit, thresholdRatio*100.0,
		)}
	}
	return nil
}

// transactionID is the transaction an update or a delete names.
func transactionID(args map[string]interface{}) (uint, error) {
	id := parseInt(args["transaction_id"], 0)
	if id <= 0 {
		return 0, fmt.Errorf("transaction_id is required to change or delete a transaction")
	}
	return uint(id), nil
}

// notYours is the refusal for a transaction that is not the caller's own: another member's, an
// owner's caller included, or none at all (BL-66).
func notYours(id uint) error {
	return fmt.Errorf("transaction %d is not one of your transactions: only the person who logged a transaction can change or delete it", id)
}

// executeUpdateTransaction changes the caller's own transaction (BL-66): whether it is shared,
// its amount, category, merchant, date, type, currency or notes. The budget and anomaly checks then
// run on the new values; the change is announced and undoable like any other.
func (m *Module) executeUpdateTransaction(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
	id, err := transactionID(args)
	if err != nil {
		return "", err
	}
	if _, ok := args["amount"]; ok && parseFloat(args["amount"], 0) <= 0 {
		return "", fmt.Errorf("transaction amount must be greater than 0")
	}
	if d := parseString(args["transaction_date"], ""); d != "" {
		if _, err := parseFlexDate(d); err != nil {
			return "", fmt.Errorf("transaction_date: %w", err)
		}
	}
	before, after, err := repo.UpdateOwnTransaction(ctx, s, id, func(tx *models.FinancialTransaction) {
		if _, ok := args["amount"]; ok {
			tx.Amount = parseFloat(args["amount"], tx.Amount)
		}
		if c := parseString(args["category"], ""); c != "" {
			tx.Category = c
		}
		if v, ok := args["merchant"]; ok {
			tx.Merchant = parseString(v, tx.Merchant)
		}
		if t := parseString(args["type"], ""); t != "" {
			tx.Type = t
		}
		if c := parseString(args["currency"], ""); c != "" {
			tx.Currency = c
		}
		if v, ok := args["notes"]; ok {
			tx.Notes = parseString(v, tx.Notes)
		}
		if d := parseString(args["transaction_date"], ""); d != "" {
			tx.TransactionDate = parseDateOrDefault(d, tx.TransactionDate)
		}
		if v, ok := args["is_shared"]; ok {
			tx.IsShared = parseBool(v, tx.IsShared)
		}
	})
	if errors.Is(err, db.ErrNotOwnTransaction) {
		return "", notYours(id)
	}
	if err != nil {
		return "", err
	}

	warnings := anomalyWarnings(ctx, repo, s, after.Type, after.Category, after.Merchant, after.Currency, after.Amount, after.ID)
	warnings = append(warnings, budgetWarnings(ctx, repo, s, after)...)

	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      "update",
		EntityType:  "FinancialTransaction",
		EntityID:    strconv.FormatUint(uint64(after.ID), 10),
		ActorUserID: m.resolveUserID(callCtx),
		HouseholdID: s.HouseholdID(),
		FamilyGroup: s.LegacyLabel(),
		BeforeJSON:  models.Snapshot(before),
		Summary:     fmt.Sprintf("Changed %s %s %.2f for %s (%s)", after.Type, after.Currency, after.Amount, after.Category, after.Merchant),
		Timestamp:   time.Now(),
	}, after)

	return describeTransaction(fmt.Sprintf("✅ Transaction %d updated:\n", after.ID), after, true, warnings), nil
}

// executeDeleteTransaction deletes the caller's own transaction (BL-66); /undo brings it back.
func (m *Module) executeDeleteTransaction(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
	id, err := transactionID(args)
	if err != nil {
		return "", err
	}
	deleted, err := repo.DeleteOwnTransaction(ctx, s, id)
	if errors.Is(err, db.ErrNotOwnTransaction) {
		return "", notYours(id)
	}
	if err != nil {
		return "", err
	}
	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      "delete",
		EntityType:  "FinancialTransaction",
		EntityID:    strconv.FormatUint(uint64(deleted.ID), 10),
		ActorUserID: m.resolveUserID(callCtx),
		HouseholdID: s.HouseholdID(),
		FamilyGroup: s.LegacyLabel(),
		BeforeJSON:  models.Snapshot(deleted),
		Summary:     fmt.Sprintf("Deleted %s %s %.2f for %s (%s)", deleted.Type, deleted.Currency, deleted.Amount, deleted.Category, deleted.Merchant),
		Timestamp:   time.Now(),
	}, nil)

	return fmt.Sprintf("🗑️ Transaction %d deleted: %s %s %.2f for %s (%s). Say /undo to bring it back.",
		deleted.ID, deleted.Type, deleted.Currency, deleted.Amount, deleted.Category, deleted.Merchant), nil
}

// executeGetSummary produces an aggregated financial summary.
func (m *Module) executeGetSummary(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
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

	userID := ""
	if ref := parseString(args["user_id"], ""); ref != "" {
		resolved, err := m.resolveMemberUserID(ctx, callCtx, ref)
		if err != nil {
			return "", err
		}
		userID = resolved
	}
	targetCategory := models.NormalizeFinanceCategory(parseString(args["category"], ""))

	summary, err := repo.GetFinancialSummary(ctx, s, userID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("failed to generate financial summary: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### 📊 Financial Summary (%s to %s)\n",
		summary.StartDate.Format("2006-01-02"), summary.EndDate.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Family Group: %s\n", s.HouseholdName()))
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
func (m *Module) executeSetBudget(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
	category := parseString(args["category"], "")
	if strings.TrimSpace(category) == "" {
		return "", fmt.Errorf("category is required")
	}
	category = models.NormalizeFinanceCategory(category)

	limit := parseFloat(args["monthly_limit"], 0)
	if limit <= 0 {
		return "", fmt.Errorf("monthly_limit must be greater than 0")
	}

	currency := strings.ToUpper(parseString(args["currency"], s.Currency("USD")))
	if currency == "" {
		currency = s.Currency("USD")
	}

	threshold := parseFloat(args["alert_threshold_pct"], 80.0)
	if threshold <= 0 {
		threshold = 80.0
	}

	budget := &models.CategoryBudget{
		HouseholdID:       s.HouseholdID(),
		FamilyGroup:       s.LegacyLabel(),
		Category:          category,
		MonthlyLimit:      limit,
		Currency:          currency,
		AlertThresholdPct: threshold,
	}

	// BL-68: whether the budget existed decides how undo reverses the change.
	existing, err := repo.GetCategoryBudget(ctx, s, category)
	if err != nil {
		return "", fmt.Errorf("failed to check category budget: %w", err)
	}
	if err := repo.SetCategoryBudget(ctx, s, budget); err != nil {
		return "", fmt.Errorf("failed to set category budget: %w", err)
	}
	action, beforeJSON := "create", ""
	if existing != nil {
		action, beforeJSON = "update", models.Snapshot(existing)
	}

	m.emitMutation(ctx, callCtx, events.MutationEvent{
		Topic:       "finance",
		Action:      action,
		Kind:        "budget_set",
		EntityType:  "CategoryBudget",
		EntityID:    strconv.FormatUint(uint64(budget.ID), 10),
		ActorUserID: m.resolveUserID(callCtx),
		HouseholdID: s.HouseholdID(),
		FamilyGroup: s.LegacyLabel(),
		BeforeJSON:  beforeJSON,
		Summary:     fmt.Sprintf("Set %s monthly budget to %s %.2f (alert at %.0f%%)", budget.Category, budget.Currency, budget.MonthlyLimit, budget.AlertThresholdPct),
		Timestamp:   time.Now(),
	}, budget)

	return fmt.Sprintf("✅ Monthly budget for '%s' successfully set to %s %.2f (alert threshold: %.1f%%).",
		budget.Category, budget.Currency, budget.MonthlyLimit, budget.AlertThresholdPct), nil
}

// executeManageBills handles recurring bill list, add, update, and delete actions.
func (m *Module) executeManageBills(ctx context.Context, callCtx orchestrator.CallContext, repo db.FinanceRepository, s identity.Scope, args map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(parseString(args["action"], "list")))

	switch action {
	case "list":
		bills, err := repo.ListRecurringBills(ctx, s)
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
		currency := strings.ToUpper(parseString(args["currency"], s.Currency("USD")))
		if currency == "" {
			currency = s.Currency("USD")
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
			HouseholdID:    s.HouseholdID(),
			FamilyGroup:    s.LegacyLabel(),
			Name:           name,
			Category:       category,
			ExpectedAmount: amount,
			Currency:       currency,
			BillingCycle:   cycle,
			NextDueDay:     dueDay,
			AutoRenew:      autoRenew,
		}

		if err := repo.CreateRecurringBill(ctx, s, bill); err != nil {
			return "", fmt.Errorf("failed to create recurring bill: %w", err)
		}

		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "create",
			Kind:        "recurring_bill_added",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(bill.ID), 10),
			ActorUserID: m.resolveUserID(callCtx),
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
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
		existing, err := repo.GetRecurringBill(ctx, s, id)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve recurring bill %d: %w", id, err)
		}
		isAuthorized := existing != nil && existing.HouseholdID != "" && existing.HouseholdID == s.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("recurring bill %d not found or permission denied", id)
		}
		beforeJSON := models.Snapshot(existing) // the snapshot /undo restores (BL-68)

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

		if err := repo.UpdateRecurringBill(ctx, s, existing); err != nil {
			return "", fmt.Errorf("failed to update recurring bill: %w", err)
		}

		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "update",
			Kind:        "recurring_bill_updated",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(existing.ID), 10),
			ActorUserID: m.resolveUserID(callCtx),
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			BeforeJSON:  beforeJSON,
			Summary:     fmt.Sprintf("Updated recurring bill %s (ID %d)", existing.Name, existing.ID),
			Timestamp:   time.Now(),
		}, existing)

		return fmt.Sprintf("✅ Recurring bill '%s' (ID: %d) successfully updated.", existing.Name, existing.ID), nil

	case "delete":
		id := uint(parseInt(args["bill_id"], 0))
		if id == 0 {
			return "", fmt.Errorf("bill_id is required to delete a recurring bill")
		}
		existing, err := repo.GetRecurringBill(ctx, s, id)
		if err != nil {
			return "", fmt.Errorf("failed to check recurring bill %d: %w", id, err)
		}
		isAuthorized := existing != nil && existing.HouseholdID != "" && existing.HouseholdID == s.HouseholdID()
		if !isAuthorized {
			return "", fmt.Errorf("recurring bill %d not found or permission denied", id)
		}

		if err := repo.DeleteRecurringBill(ctx, s, id); err != nil {
			return "", fmt.Errorf("failed to delete recurring bill: %w", err)
		}

		beforeJSON := models.Snapshot(existing) // the snapshot /undo restores (BL-68)
		m.emitMutation(ctx, callCtx, events.MutationEvent{
			Topic:       "finance",
			Action:      "delete",
			Kind:        "recurring_bill_deleted",
			EntityType:  "RecurringBill",
			EntityID:    strconv.FormatUint(uint64(id), 10),
			ActorUserID: m.resolveUserID(callCtx),
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			BeforeJSON:  beforeJSON,
			Summary:     fmt.Sprintf("Deleted recurring bill %s (ID %d)", existing.Name, id),
			Timestamp:   time.Now(),
		}, existing)

		return fmt.Sprintf("✅ Recurring bill '%s' (ID: %d) deleted.", existing.Name, id), nil

	default:
		return "", fmt.Errorf("unsupported action '%s', expected 'list', 'add', 'update', or 'delete'", action)
	}
}
