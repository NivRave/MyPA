package schedule

import (
	"google.golang.org/genai"
)

// Declarations returns the Gemini FunctionDeclaration schemas for schedule tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "schedule_topic_event",
			Description: "Create a topic-tagged schedule event with virtual sub-calendar support. Events can be synchronized with Google Calendar (with topic prefix e.g. [Baby], [House] and custom color coding) or kept internal-only for high-frequency routines (baby feedings, meds, workouts). If shared, also synchronizes to partner's calendar.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "Title or summary of the event (e.g. 'Pediatrician Checkup', 'Plumber Visit', 'Morning Feeding').",
					},
					"topic": {
						Type:        genai.TypeString,
						Description: "Life domain or topic (e.g. 'baby', 'house', 'finance', 'health', 'work', 'personal', 'family').",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start time in ISO 8601 / RFC3339 format (e.g. '2026-11-04T10:00:00Z').",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Optional end time in ISO 8601 / RFC3339 format. If omitted, defaults to 1 hour after start time.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Optional notes or details about the event.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional sub-tags for filtering (e.g. ['pediatrician', 'vaccine'], ['maintenance', 'plumber']).",
					},
					"sync_to_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to sync to Google Calendar. Set to false for internal-only schedules that shouldn't clutter the primary calendar.",
					},
					"is_shared": {
						Type:        genai.TypeBoolean,
						Description: "Whether this event is shared with family/partner. When true, syncs to partner's Google calendar as well.",
					},
					"recurrence": {
						Type:        genai.TypeString,
						Description: "Optional recurrence rule in RRULE format (e.g. 'RRULE:FREQ=DAILY;INTERVAL=1').",
					},
				},
				Required: []string{"title", "topic", "start_time"},
			},
		},
		{
			Name:        "list_topic_schedule",
			Description: "Query and list schedule events filtered by topic, tags, and time range. Merges database schedule events with Google Calendar events.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Topic to filter by (e.g. 'baby', 'house', 'finance', 'work', 'all').",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional tags to filter by.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start time range in ISO 8601 / RFC3339 format. Defaults to start of today.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "End time range in ISO 8601 / RFC3339 format. Defaults to 30 days ahead.",
					},
					"include_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to merge events directly from Google Calendar (default true).",
					},
				},
			},
		},
		{
			Name:        "configure_topic_calendar",
			Description: "Customize virtual sub-calendar settings for a topic: custom bracket prefix tag, Google Calendar color ID (1-11), internal-only vs Google sync default, and morning briefing inclusion.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Topic to configure (e.g. 'baby', 'house', 'finance', 'health', 'work').",
					},
					"prefix_tag": {
						Type:        genai.TypeString,
						Description: "Custom prefix bracket tag for calendar event titles (e.g. 'Baby' results in '[Baby] Event Title').",
					},
					"color_id": {
						Type:        genai.TypeString,
						Description: "Google Calendar event color ID (e.g. '1' for Lavender, '2' for Sage, '4' for Flamingo, etc.).",
					},
					"target_calendar_id": {
						Type:        genai.TypeString,
						Description: "Optional Google Calendar ID if routing events to a dedicated sub-calendar instead of 'primary'.",
					},
					"sync_to_google_default": {
						Type:        genai.TypeBoolean,
						Description: "Default sync_to_google setting for this topic (true = sync to Google, false = internal database only).",
					},
					"include_in_morning_briefing": {
						Type:        genai.TypeBoolean,
						Description: "Whether events in this topic should be summarized in the daily morning briefing.",
					},
				},
				Required: []string{"topic"},
			},
		},
		{
			Name:        "delete_topic_event",
			Description: "Delete a topic-tagged schedule event by ID, removing it from the database and any synced Google Calendars (including partner's calendar).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"event_id": {
						Type:        genai.TypeInteger,
						Description: "The numeric ID of the schedule event to delete.",
					},
				},
				Required: []string{"event_id"},
			},
		},
		{
			Name:        "update_topic_event",
			Description: "Update an existing schedule event's title, time, notes, tags, or sync status across database and Google Calendars.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"event_id": {
						Type:        genai.TypeInteger,
						Description: "The numeric ID of the schedule event to update.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "Updated event title.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Updated start time in ISO 8601 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Updated end time in ISO 8601 format.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Updated description or notes.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Updated tags.",
					},
				},
				Required: []string{"event_id"},
			},
		},
	}
}
