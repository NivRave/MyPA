package finance

import (
	"google.golang.org/genai"
)

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
						Description: "3-letter currency code (e.g. 'USD', 'ILS', 'EUR'). Defaults to the household currency (USD if none is set).",
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
						Description: "Currency code (default: the household currency, else 'USD').",
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
						Description: "Currency code (default: the household currency, else 'USD').",
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
