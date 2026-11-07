package workspace_calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// CalendarFactory creates or retrieves a CalendarClient for a specific user.
type CalendarFactory func(ctx context.Context, userID string) (orchestrator.CalendarClient, error)

// Option configures the workspace_calendar Module.
type Option func(*Module)

// WithEventBus sets the event bus for mutation emissions.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for Google Calendar operations.
type Module struct {
	factory CalendarFactory
	bus     *events.EventBus
}

// NewModule initializes the workspace_calendar module.
func NewModule(factory CalendarFactory, opts ...Option) *Module {
	m := &Module{factory: factory}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Module) Name() string {
	return "workspace_calendar"
}

func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return ""
}

func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
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
						Type: genai.TypeArray,
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
						Type: genai.TypeArray,
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
	}
}

func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	if m.factory == nil {
		return "⚠️ Google Calendar integration is not configured.", nil
	}

	actorID := callCtx.ActorID()
	calClient, err := m.factory(ctx, actorID)
	if err != nil {
		if strings.EqualFold(err.Error(), "unauthorized") {
			return "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.", nil
		}
		return "⚠️ Your Google connection expired or is invalid. Please send /connect again.", nil
	}

	bus := m.bus
	if bus == nil {
		bus = callCtx.EventBus
	}

	switch toolName {
	case "create_calendar_event":
		argsJSON, _ := json.Marshal(args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return fmt.Sprintf("failed to parse tool arguments into event: %v", err), nil
		}

		link, err := calClient.CreateEvent(ctx, event)
		if err != nil {
			return "❌ Failed to create event: " + err.Error(), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "schedule",
			Action:      "create",
			EntityType:  "CalendarEvent",
			EntityID:    link,
			ActorUserID: actorID,
			FamilyGroup: callCtx.TenantFamilyGroup(),
			AfterJSON:   string(argsJSON),
			Summary:     fmt.Sprintf("Created calendar event '%s'", event.Title),
		})

		return fmt.Sprintf("Event Created Successfully! Link: %s", link), nil

	case "update_calendar_event":
		argsJSON, _ := json.Marshal(args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return fmt.Sprintf("failed to parse tool arguments into event: %v", err), nil
		}

		err = calClient.UpdateEvent(ctx, event.ID, event)
		if err != nil {
			return "❌ Failed to update event: " + err.Error(), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "schedule",
			Action:      "update",
			EntityType:  "CalendarEvent",
			EntityID:    event.ID,
			ActorUserID: actorID,
			FamilyGroup: callCtx.TenantFamilyGroup(),
			AfterJSON:   string(argsJSON),
			Summary:     fmt.Sprintf("Updated calendar event '%s'", event.ID),
		})

		return "Event Updated Successfully!", nil

	case "delete_calendar_event":
		id := toolutil.String(args, "id")
		if id == "" {
			return "Missing id parameter", nil
		}

		err = calClient.DeleteEvent(ctx, id)
		if err != nil {
			return "❌ Failed to delete event: " + err.Error(), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "schedule",
			Action:      "delete",
			EntityType:  "CalendarEvent",
			EntityID:    id,
			ActorUserID: actorID,
			FamilyGroup: callCtx.TenantFamilyGroup(),
			Summary:     fmt.Sprintf("Deleted calendar event '%s'", id),
		})

		return "Event Deleted Successfully!", nil

	case "list_calendar_events":
		timeMin := toolutil.String(args, "time_min")
		timeMax := toolutil.String(args, "time_max")
		query := toolutil.String(args, "query")

		eventsList, err := calClient.ListEvents(ctx, timeMin, timeMax, query)
		if err != nil {
			return "❌ Failed to fetch calendar events: " + err.Error(), nil
		}

		eventsJSON, _ := json.Marshal(eventsList)
		return fmt.Sprintf("Here are the calendar events I found:\n%s", string(eventsJSON)), nil

	default:
		return "", fmt.Errorf("unknown tool %s in workspace_calendar", toolName)
	}
}
