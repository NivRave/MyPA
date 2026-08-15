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
					"recurrence": {
						Type:        genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional recurrence rules for repeating events. Must be an array of RFC 5545 RRULE strings. For example: ['RRULE:FREQ=WEEKLY;BYDAY=TU'] for every Tuesday.",
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
					"recurrence": {
						Type:        genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional recurrence rules for repeating events. Must be an array of RFC 5545 RRULE strings. For example: ['RRULE:FREQ=WEEKLY;BYDAY=TU'] for every Tuesday.",
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
		{
			Name:        "remember_fact",
			Description: "Saves a personal fact, preference, or important detail about the user into long-term memory so you can recall it in future conversations.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"fact": {
						Type:        genai.TypeString,
						Description: "The exact fact or preference to remember (e.g. 'The user is vegan', 'The user's wife is named Sarah', 'The user prefers meetings in the afternoon').",
					},
				},
				Required: []string{"fact"},
			},
		},
		{
			Name:        "list_unread_emails",
			Description: "Lists the user's recent unread emails. Use this to check for new messages.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"max_results": {
						Type:        genai.TypeInteger,
						Description: "The maximum number of emails to fetch (default: 5).",
					},
				},
			},
		},
		{
			Name:        "read_email",
			Description: "Reads the contents of a specific email by its ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_id": {
						Type:        genai.TypeString,
						Description: "The ID of the email to read.",
					},
				},
				Required: []string{"message_id"},
			},
		},
		{
			Name:        "draft_email_reply",
			Description: "Drafts a reply to an email. Does not send it.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_id": {
						Type:        genai.TypeString,
						Description: "The ID of the email to reply to.",
					},
					"reply_text": {
						Type:        genai.TypeString,
						Description: "The body of the reply.",
					},
				},
				Required: []string{"message_id", "reply_text"},
			},
		},
		{
			Name:        "list_tasks",
			Description: "Lists the user's pending Google Tasks from their default task list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "create_task",
			Description: "Creates a new task on the user's Google Tasks list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The title of the task.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Optional additional notes for the task.",
					},
					"due": {
						Type:        genai.TypeString,
						Description: "Optional due date for the task in RFC 3339 format (e.g., '2023-10-01T00:00:00Z').",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "complete_task",
			Description: "Marks a task as completed in Google Tasks.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"task_id": {
						Type:        genai.TypeString,
						Description: "The ID of the task to complete.",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "delete_task",
			Description: "Deletes a task entirely from Google Tasks.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"task_id": {
						Type:        genai.TypeString,
						Description: "The ID of the task to delete.",
					},
				},
				Required: []string{"task_id"},
			},
		},
	},
}
