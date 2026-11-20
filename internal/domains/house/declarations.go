package house

import (
	"google.golang.org/genai"
)

// Declarations provides Gemini FunctionDeclaration schemas for house and vehicle tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "save_house_contract_or_receipt",
			Description: "Store or update house leases, mortgages, utility contracts, appliance warranties, insurance policies, and repair receipts.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"contract_id": {
						Type:        genai.TypeInteger,
						Description: "Optional numeric ID of an existing contract or receipt to update.",
					},
					"record_type": {
						Type:        genai.TypeString,
						Description: "Type of record: 'lease', 'mortgage', 'utility', 'warranty', 'insurance', or 'receipt'.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "Descriptive title (e.g., 'Apartment Lease 2026', 'Refrigerator 5-Year Warranty', 'Homeowners Insurance Policy', 'Plumbing Invoice').",
					},
					"vendor_or_landlord": {
						Type:        genai.TypeString,
						Description: "Name of the landlord, HOA, utility provider, insurer, or contractor/merchant.",
					},
					"contact_details": {
						Type:        genai.TypeString,
						Description: "Phone, email, or contact info for landlord or vendor.",
					},
					"amount": {
						Type:        genai.TypeNumber,
						Description: "Recurring payment amount or invoice total (e.g. 5200.0).",
					},
					"currency": {
						Type:        genai.TypeString,
						Description: "Currency code (e.g. 'USD', 'EUR', 'ILS'). Defaults to the household's currency, else 'USD'.",
					},
					"start_date": {
						Type:        genai.TypeString,
						Description: "Start date in ISO 8601 / YYYY-MM-DD format.",
					},
					"expiration_date": {
						Type:        genai.TypeString,
						Description: "Expiration or lease renewal date in ISO 8601 / YYYY-MM-DD format.",
					},
					"notice_period_days": {
						Type:        genai.TypeInteger,
						Description: "Required notice period in days prior to expiration/renewal (e.g., 60 days for lease renewal).",
					},
					"key_terms": {
						Type:        genai.TypeString,
						Description: "Key clauses, warranty conditions, or JSON object with terms (e.g., 'Security deposit: 2 months, No subletting').",
					},
				},
				Required: []string{"record_type", "title"},
			},
		},
		{
			Name:        "list_house_contracts",
			Description: "List and search house contracts, leases, warranties, insurance policies, or receipts with optional expiration and category filters.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"record_type": {
						Type:        genai.TypeString,
						Description: "Filter by record type: 'lease', 'mortgage', 'utility', 'warranty', 'insurance', 'receipt', or 'all'.",
					},
					"expiring_within_days": {
						Type:        genai.TypeInteger,
						Description: "Query contracts or warranties expiring within N days from now (e.g. 30, 60, 90).",
					},
					"search_query": {
						Type:        genai.TypeString,
						Description: "Optional keyword to search within title or vendor/landlord name.",
					},
				},
			},
		},
		{
			Name:        "manage_home_maintenance",
			Description: "Manage preventive home upkeep schedules (HVAC, plumbing, appliances, pest control), log completions, list overdue tasks, or get seasonal suggestions.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to execute: 'create', 'complete', 'list', 'update', 'delete', or 'suggest_seasonal'.",
					},
					"item_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the maintenance item (required for 'complete', 'update', 'delete').",
					},
					"task_name": {
						Type:        genai.TypeString,
						Description: "Name of the task (e.g., 'Clean HVAC air filters', 'Flush water heater', 'Descale espresso machine', 'Exterminator inspection').",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Maintenance category: 'hvac', 'plumbing', 'appliance', 'pest_control', or 'general'.",
					},
					"interval_days": {
						Type:        genai.TypeInteger,
						Description: "Recurrence interval in days (e.g. 90 for quarterly, 180 for biannual, 365 for annual).",
					},
					"next_due_at": {
						Type:        genai.TypeString,
						Description: "Explicit next due date in YYYY-MM-DD or RFC3339 format.",
					},
					"contractor_info": {
						Type:        genai.TypeString,
						Description: "Preferred service provider, phone number, or technician name.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Instructions, filter part numbers, or upkeep notes.",
					},
					"overdue_only": {
						Type:        genai.TypeBoolean,
						Description: "If true, only return overdue maintenance items during 'list'.",
					},
				},
				Required: []string{"action"},
			},
		},
		{
			Name:        "manage_vehicle_asset",
			Description: "Log and track family vehicles, annual licensing/test dates, insurance policy expiry, odometer mileage, and service maintenance history.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to execute: 'create', 'update', 'list', 'delete', 'update_mileage', or 'log_service'.",
					},
					"asset_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the vehicle asset (required for 'update', 'delete', 'update_mileage', 'log_service').",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "Vehicle description (e.g. 'Toyota RAV4 2022', 'Family Mazda CX-5').",
					},
					"license_plate": {
						Type:        genai.TypeString,
						Description: "Vehicle license plate number.",
					},
					"insurance_expiry": {
						Type:        genai.TypeString,
						Description: "Car insurance policy expiration date in YYYY-MM-DD or RFC3339 format.",
					},
					"annual_test_expiry": {
						Type:        genai.TypeString,
						Description: "Annual vehicle inspection / licensing test expiration date in YYYY-MM-DD or RFC3339 format.",
					},
					"last_service_date": {
						Type:        genai.TypeString,
						Description: "Date of last mechanic service in YYYY-MM-DD or RFC3339 format.",
					},
					"current_mileage_km": {
						Type:        genai.TypeInteger,
						Description: "Current odometer reading in kilometers.",
					},
					"next_service_mileage_km": {
						Type:        genai.TypeInteger,
						Description: "Target odometer reading for next scheduled service in kilometers.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Vehicle details, tire specifications, or service notes.",
					},
				},
				Required: []string{"action"},
			},
		},
	}
}
