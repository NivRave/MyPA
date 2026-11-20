package preferences

import (
	"google.golang.org/genai"
)

// Declarations returns the Gemini FunctionDeclarations provided by this module: the person's
// preferences and the household's personal CRM, both Core (V4-27).
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "update_user_preferences",
			Description: "Update runtime user preferences, persona tone, default currency, quiet hours, briefing topics, and custom standing instructions for the assistant.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"preferred_language": {
						Type:        genai.TypeString,
						Description: "User's preferred language for replies (e.g. 'English', 'Hebrew', 'Spanish').",
					},
					"tone_persona": {
						Type:        genai.TypeString,
						Description: "Assistant persona tone (e.g. 'Concise & Direct', 'Warm & Encouraging', 'Humorous', 'Analytical').",
					},
					"default_currency": {
						Type:        genai.TypeString,
						Description: "Default currency code (e.g. 'ILS', 'USD', 'EUR').",
					},
					"quiet_hours_start": {
						Type:        genai.TypeString,
						Description: "Start of quiet hours in 24h format (e.g. '22:00').",
					},
					"quiet_hours_end": {
						Type:        genai.TypeString,
						Description: "End of quiet hours in 24h format (e.g. '07:30').",
					},
					"briefing_topics": {
						Type:        genai.TypeString,
						Description: "JSON array of topics to include in morning briefings: 'schedule' and the household's topics (e.g. '[\"schedule\", \"family\", \"health\"]').",
					},
					"standing_instructions": {
						Type:        genai.TypeString,
						Description: "Permanent standing instructions from the user dynamically injected into every LLM prompt turn.",
					},
				},
			},
		},
		{
			Name:        "get_user_preferences",
			Description: "Retrieve current user customization preferences, persona settings, and standing instructions.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "upsert_personal_crm",
			Description: "Save or update a personal/family CRM contact with important dates (birthdays, anniversaries), gift ideas, clothing/shoe sizes, and medical notes.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeInteger,
						Description: "Optional contact ID if updating an existing contact.",
					},
					"person_name": {
						Type:        genai.TypeString,
						Description: "Full or familiar name of the person (e.g. 'Grandma Sarah', 'Alice', 'Dr. Cohen').",
					},
					"relationship": {
						Type:        genai.TypeString,
						Description: "Relationship to user/family (e.g. 'Mother', 'Sister', 'Spouse', 'Pediatrician', 'Friend').",
					},
					"important_dates": {
						Type:        genai.TypeString,
						Description: "JSON array of important dates (e.g. '[{\"label\": \"Birthday\", \"date\": \"1988-11-12\"}, {\"label\": \"Anniversary\", \"date\": \"2018-06-20\"}]').",
					},
					"gift_ideas_and_sizes": {
						Type:        genai.TypeString,
						Description: "Gift preferences, clothing sizes, shoe sizes, hobbies, or wish list items.",
					},
					"medical_notes": {
						Type:        genai.TypeString,
						Description: "Important medical notes, allergies, prescriptions, or dietary restrictions.",
					},
				},
				Required: []string{"person_name"},
			},
		},
		{
			Name:        "query_personal_crm",
			Description: "Search personal and family CRM contacts by name, relationship, gift ideas, or medical notes.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "Search term to match across names, relationships, gift notes, or medical notes.",
					},
					"relationship": {
						Type:        genai.TypeString,
						Description: "Optional relationship filter.",
					},
				},
			},
		},
	}
}
