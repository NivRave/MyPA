package baby

import (
	"google.golang.org/genai"
)

// Declarations returns the Gemini FunctionDeclaration schemas for baby tracker tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "upsert_baby_profile",
			Description: "Create or update a baby/child profile with name, date of birth, due date, birth weight, current height/weight, pediatrician details, and allergies.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeInteger,
						Description: "Optional numeric ID of an existing baby profile to update.",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "The baby's name (e.g. 'Leo', 'Maya').",
					},
					"date_of_birth": {
						Type:        genai.TypeString,
						Description: "Date of birth in YYYY-MM-DD or RFC3339 format (e.g. '2026-06-15').",
					},
					"due_date": {
						Type:        genai.TypeString,
						Description: "Optional original due date (crucial for calculating gestational adjusted age if baby was born premature).",
					},
					"gender": {
						Type:        genai.TypeString,
						Description: "Gender: 'male', 'female', or 'other'.",
					},
					"birth_weight_grams": {
						Type:        genai.TypeInteger,
						Description: "Birth weight in grams (e.g. 3400).",
					},
					"current_weight_grams": {
						Type:        genai.TypeInteger,
						Description: "Most recently measured weight in grams (e.g. 5800).",
					},
					"height_cm": {
						Type:        genai.TypeNumber,
						Description: "Height or length in centimeters (e.g. 62.5).",
					},
					"pediatrician_info": {
						Type:        genai.TypeString,
						Description: "Pediatrician name, clinic, or contact phone.",
					},
					"allergies_notes": {
						Type:        genai.TypeString,
						Description: "Known allergies, sensitivities, or special medical instructions.",
					},
				},
				Required: []string{"name", "date_of_birth"},
			},
		},
		{
			Name:        "log_baby_care",
			Description: "Log a baby care event: feeding (ml/breast), diaper (wet/dirty/both), sleep duration, temperature, medication (with automatic safety cooldown check), milestone, or growth measurement.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby. If omitted and only one baby exists in the family group, it is automatically resolved.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby (used to look up the baby if baby_id is unknown).",
					},
					"log_type": {
						Type:        genai.TypeString,
						Description: "Type of care event: 'feeding', 'sleep', 'diaper', 'temperature', 'medication', 'milestone', or 'measurement'.",
					},
					"value_numeric": {
						Type:        genai.TypeNumber,
						Description: "Numeric measurement value (e.g., 120 for feeding ml, 1.5 for sleep hours, 38.2 for temperature, 6200 for weight in grams).",
					},
					"unit": {
						Type:        genai.TypeString,
						Description: "Unit of measurement: 'ml', 'oz', 'hours', 'minutes', 'celsius', 'fahrenheit', 'kg', 'g', 'cm', 'doses'.",
					},
					"details": {
						Type:        genai.TypeString,
						Description: "Descriptive details (e.g., 'formula', 'breastmilk left breast', 'wet and dirty', 'Paracetamol 2.5ml', 'rolled tummy to back').",
					},
					"timestamp": {
						Type:        genai.TypeString,
						Description: "Time of the event in RFC3339 or ISO 8601 format (e.g. '2026-11-03T14:30:00Z'). Defaults to current time if omitted.",
					},
					"force": {
						Type:        genai.TypeBoolean,
						Description: "Optional override flag to force logging medication even if pediatric safety cooldown is active.",
					},
				},
				Required: []string{"log_type"},
			},
		},
		{
			Name:        "query_baby_logs_and_trends",
			Description: "Query baby care logs and aggregated trend statistics (total feeding volume, sleep totals, diaper count, last medication, fever checks) over a time range (e.g. today, last 24h, 3 days, 7 days).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby to query.",
					},
					"log_type": {
						Type:        genai.TypeString,
						Description: "Filter by log type ('feeding', 'sleep', 'diaper', 'temperature', 'medication', 'milestone', 'measurement', or 'all'). Default 'all'.",
					},
					"time_range": {
						Type:        genai.TypeString,
						Description: "Convenience time period: 'today', '24h', '3d', '7d', '14d', '30d'. Default '24h'.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Explicit start time filter in RFC3339 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Explicit end time filter in RFC3339 format.",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Maximum number of recent raw log entries to return (default 50).",
					},
				},
			},
		},
		{
			Name:        "get_baby_milestone_suggestions",
			Description: "Get developmental milestone insights, current Wonder Weeks cognitive leap, upcoming vaccination schedule, recommended pediatric checkups, feeding transitions, and wake windows based on exact age.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"baby_id": {
						Type:        genai.TypeInteger,
						Description: "Numeric ID of the baby.",
					},
					"baby_name": {
						Type:        genai.TypeString,
						Description: "Name of the baby.",
					},
					"category": {
						Type:        genai.TypeString,
						Description: "Filter guidance category: 'all', 'development', 'vaccine', 'checkup', 'feeding', or 'sleep'. Default 'all'.",
					},
				},
			},
		},
		{
			Name:        "manage_baby_inventory",
			Description: "Track and manage baby supplies (formula, diapers, wipes, Vitamin D drops). View stock levels, record consumption or restocks, and check low-threshold alerts.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to perform: 'list', 'get', 'set', 'adjust', or 'check_low'.",
					},
					"item_name": {
						Type:        genai.TypeString,
						Description: "Name of the supply item (e.g. 'Formula Stage 1', 'Diapers Size 2', 'Vitamin D Drops', 'Wipes').",
					},
					"quantity": {
						Type:        genai.TypeNumber,
						Description: "Exact quantity remaining when action is 'set'.",
					},
					"delta": {
						Type:        genai.TypeNumber,
						Description: "Amount to adjust quantity by when action is 'adjust' (e.g. -1 for using 1 can, +3 for restock).",
					},
					"unit": {
						Type:        genai.TypeString,
						Description: "Unit of item (e.g. 'cans', 'packs', 'boxes', 'bottles', 'drops').",
					},
					"low_threshold": {
						Type:        genai.TypeNumber,
						Description: "Alert threshold when remaining stock falls at or below this value.",
					},
				},
				Required: []string{"action"},
			},
		},
	}
}
