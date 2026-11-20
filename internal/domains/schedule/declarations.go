package schedule

import (
	"google.golang.org/genai"
)

// recurrenceSchema is one or more RFC 5545 recurrence lines (a single string is accepted too).
var recurrenceSchema = &genai.Schema{
	Type:        genai.TypeArray,
	Items:       &genai.Schema{Type: genai.TypeString},
	Description: "Optional recurrence rules for repeating events, as RFC 5545 RRULE strings. For example: ['RRULE:FREQ=WEEKLY;BYDAY=TU'] for every Tuesday.",
}

// timezoneSchema says how a time without an offset is read.
var timezoneSchema = &genai.Schema{
	Type:        genai.TypeString,
	Description: "The user's IANA time zone (e.g. 'Asia/Jerusalem'). A time without an offset is read in this zone, and Google Calendar shows the event in it.",
}

// eventRefSchemas are the two ways to name an event: MyPA's ID, or Google's for an event made in Google.
var eventRefSchemas = map[string]*genai.Schema{
	"event_id": {
		Type:        genai.TypeInteger,
		Description: "The MyPA ID of the event, as shown by list_calendar_events.",
	},
	"google_event_id": {
		Type:        genai.TypeString,
		Description: "The Google ID of an event that exists only in Google Calendar, as shown by list_calendar_events. Use it only when the event has no MyPA ID.",
	},
}

func withEventRef(props map[string]*genai.Schema) map[string]*genai.Schema {
	for k, v := range eventRefSchemas {
		props[k] = v
	}
	return props
}

// Declarations returns the calendar domain's tools (V4-08: one set; Google sync is a flag).
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "create_calendar_event",
			Description: "Creates a calendar event. Call this when the user asks to schedule a meeting, block time, add an appointment, or create an event. The event is saved in MyPA and, unless sync_to_google is false, added to the user's Google Calendar. A topic files it under a virtual sub-calendar (its Google title gets a [Topic] prefix and the topic's colour); shared events are also added to the partner's calendar.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "Title or summary of the event (e.g. 'Pediatrician Checkup', 'Plumber Visit').",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start time in ISO 8601 format (e.g. '2026-11-04T10:00:00').",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Optional end time in ISO 8601 format. Defaults to one hour after the start.",
					},
					"timezone": timezoneSchema,
					"description": {
						Type:        genai.TypeString,
						Description: "Optional notes or details about the event.",
					},
					"topic": {
						Type:        genai.TypeString,
						Description: "Optional life domain: one of the household's topics (e.g. 'family', 'health', 'work'). Leave it out for a plain calendar event.",
					},
					"tags": {
						Type:        genai.TypeArray,
						Items:       &genai.Schema{Type: genai.TypeString},
						Description: "Optional sub-tags for filtering (e.g. ['pediatrician', 'vaccine']).",
					},
					"sync_to_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to add the event to Google Calendar (default true, or the topic's setting). Set false for internal-only routines (daily medication, workouts) that should not clutter the calendar.",
					},
					"is_shared": {
						Type:        genai.TypeBoolean,
						Description: "Whether the event is shared with the family; a shared, synced event is also added to the partner's Google Calendar.",
					},
					"recurrence": recurrenceSchema,
				},
				Required: []string{"title", "start_time", "timezone"},
			},
		},
		{
			Name:        "list_calendar_events",
			Description: "Lists the user's calendar: MyPA's events merged with Google Calendar. Call this when the user asks what is on their schedule, whether they have meetings, or to find an event. Each event shows the ID to use with update_calendar_event and delete_calendar_event.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start of the range in ISO 8601 format. Defaults to the start of today.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "End of the range in ISO 8601 format. Defaults to 30 days after the start.",
					},
					"query": {
						Type:        genai.TypeString,
						Description: "Optional free-text search terms to find specific events.",
					},
					"topic": {
						Type:        genai.TypeString,
						Description: "Optional topic to filter by: one of the household's topics (e.g. 'work') or 'all'.",
					},
					"tags": {
						Type:        genai.TypeArray,
						Items:       &genai.Schema{Type: genai.TypeString},
						Description: "Optional tags to filter by.",
					},
					"include_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to include events from Google Calendar (default true).",
					},
				},
			},
		},
		{
			Name:        "update_calendar_event",
			Description: "Changes an existing calendar event (title, time, notes, tags or recurrence) in MyPA and in Google Calendar. Call this when the user asks to change, reschedule, or modify an event. Give event_id, or google_event_id for an event that exists only in Google.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: withEventRef(map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The new title.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "The new start time in ISO 8601 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "The new end time in ISO 8601 format.",
					},
					"timezone": timezoneSchema,
					"description": {
						Type:        genai.TypeString,
						Description: "The new description or notes.",
					},
					"tags": {
						Type:        genai.TypeArray,
						Items:       &genai.Schema{Type: genai.TypeString},
						Description: "The new tags.",
					},
					"recurrence": recurrenceSchema,
				}),
			},
		},
		{
			Name:        "delete_calendar_event",
			Description: "Deletes a calendar event from MyPA and from every Google Calendar it was added to (including the partner's). Call this when the user asks to cancel, remove, or delete an event. Give event_id, or google_event_id for an event that exists only in Google.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: withEventRef(map[string]*genai.Schema{}),
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
						Description: "Topic to configure: one of the household's topics (e.g. 'health', 'work').",
					},
					"prefix_tag": {
						Type:        genai.TypeString,
						Description: "Custom prefix bracket tag for calendar event titles (e.g. 'Health' results in '[Health] Event Title').",
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
	}
}
