package llm

import "google.golang.org/genai"

// CalendarEventTool is the tool schema we provide to the LLM
// for creating a calendar event.
var CalendarEventTool = &genai.Tool{
	FunctionDeclarations: []*genai.FunctionDeclaration{
		{
			Name:        "create_calendar_event",
			Description: "Creates a new event on the user's Google Calendar. Call this when the user asks to schedule a meeting, block time, or create an event.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The title or subject of the event.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "The start time of the event in ISO 8601 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "The end time of the event in ISO 8601 format.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Optional description of the event.",
					},
					"timezone": {
						Type:        genai.TypeString,
						Description: "The IANA timezone of the user (e.g. 'America/New_York'). MUST be provided based on the user's current context.",
					},
				},
				Required: []string{"title", "start_time", "end_time", "timezone"},
			},
		},
	},
}
