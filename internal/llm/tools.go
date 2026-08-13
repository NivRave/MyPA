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
		{
			Name:        "list_calendar_events",
			Description: "Retrieves events from the user's Google Calendar. Call this when the user asks what is on their schedule, if they have meetings, or to query their calendar.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"time_min": {
						Type:        genai.TypeString,
						Description: "The start time of the query range in ISO 8601 format (e.g. '2023-10-01T00:00:00Z').",
					},
					"time_max": {
						Type:        genai.TypeString,
						Description: "The end time of the query range in ISO 8601 format.",
					},
					"query": {
						Type:        genai.TypeString,
						Description: "Optional free-text search terms to find specific events.",
					},
				},
			},
		},
		{
			Name:        "update_calendar_event",
			Description: "Updates an existing event on the user's Google Calendar. Call this when the user asks to change, reschedule, or modify an event.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeString,
						Description: "The ID of the event to update.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "The new title of the event.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "The new start time of the event in ISO 8601 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "The new end time of the event in ISO 8601 format.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "The new description of the event.",
					},
					"timezone": {
						Type:        genai.TypeString,
						Description: "The IANA timezone of the user (e.g. 'America/New_York').",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "delete_calendar_event",
			Description: "Deletes an event from the user's Google Calendar. Call this when the user asks to cancel, remove, or delete an event.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeString,
						Description: "The ID of the event to delete.",
					},
				},
				Required: []string{"id"},
			},
		},
	},
}
