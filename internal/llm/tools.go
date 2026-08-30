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
			Name:        "search_emails",
			Description: "Searches the user's Gmail using standard Gmail query syntax. Use this to find recent emails or specific threads (e.g., 'newer_than:30d', 'is:unread', 'from:john').",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The Gmail search query (e.g. 'is:unread', 'newer_than:1m').",
					},
					"max_results": {
						Type:        genai.TypeInteger,
						Description: "The maximum number of emails to fetch (default: 5, max 100).",
					},
				},
				Required: []string{"query"},
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
			Name:        "list_task_lists",
			Description: "Lists all of the user's available task lists (like 'Groceries', 'Default', etc.) and their IDs.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "create_task_list",
			Description: "Creates a new task list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The title of the new task list.",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "list_tasks",
			Description: "Lists the user's pending Google Tasks from a specific task list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list to fetch tasks from. If omitted, fetches from the default list.",
					},
				},
			},
		},
		{
			Name:        "create_task",
			Description: "Creates a new task on a specific Google Tasks list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list to create the task in. If omitted, uses the default list.",
					},
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
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list containing the task. If omitted, uses the default list.",
					},
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
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list containing the task. If omitted, uses the default list.",
					},
					"task_id": {
						Type:        genai.TypeString,
						Description: "The ID of the task to delete.",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "schedule_reminder",
			Description: "Schedules a proactive message to be sent to the user at a specific future time. Use this when the user explicitly asks to be reminded about something at a specific time (e.g. 'Remind me at 11:00').",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message": {
						Type:        genai.TypeString,
						Description: "The reminder message to send.",
					},
					"due_time": {
						Type:        genai.TypeString,
						Description: "The exact time to send the reminder in ISO 8601 format (e.g. '2023-10-01T11:00:00Z').",
					},
				},
				Required: []string{"message", "due_time"},
			},
		},
		{
			Name:        "fetch_webpage",
			Description: "Fetches and returns the main article text content of a webpage. Use this when the user sends a link and wants you to read, summarize, or save it.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"url": {
						Type:        genai.TypeString,
						Description: "The full URL of the webpage to fetch.",
					},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "archive_emails",
			Description: "Archives one or more emails by removing them from the INBOX.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails to archive.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
				},
				Required: []string{"message_ids"},
			},
		},
		{
			Name:        "soft_delete_emails",
			Description: "Moves one or more emails to the native Gmail Trash bin (soft delete).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails to delete.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
				},
				Required: []string{"message_ids"},
			},
		},
		{
			Name:        "list_email_labels",
			Description: "Lists all available Gmail labels and their IDs for the user.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "apply_email_labels",
			Description: "Applies a specific Gmail label to one or more emails.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
					"label_id": {
						Type:        genai.TypeString,
						Description: "The ID of the label to apply.",
					},
				},
				Required: []string{"message_ids", "label_id"},
			},
		},
		{
			Name:        "create_email_label",
			Description: "Creates a new Gmail label and returns its ID. Use this when you want to categorize an email and the appropriate label doesn't exist yet.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"label_name": {
						Type:        genai.TypeString,
						Description: "The name of the new label (e.g. 'Receipts', 'Newsletters').",
					},
				},
				Required: []string{"label_name"},
			},
		},
		{
			Name:        "search_contacts",
			Description: "Searches the user's Google Contacts by name. Call this when the user asks for someone's email or phone number.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The name or part of the name of the contact to search for.",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "create_contact",
			Description: "Creates a new Google Contact. Call this when the user asks to save a phone number, email, or contact information.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "The name of the person.",
					},
					"email": {
						Type:        genai.TypeString,
						Description: "The email address of the person (optional).",
					},
					"phone": {
						Type:        genai.TypeString,
						Description: "The phone number of the person (optional).",
					},
				},
				Required: []string{"name"},
			},
		},
	},
}
