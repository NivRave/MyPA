package workspace_calendar

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/api/calendar/v3"
)

type mockCalClient struct {
	createdEvent models.CalendarEvent
	updatedEvent models.CalendarEvent
	deletedID    string
	listQuery    string
	createErr    error
}

func (m *mockCalClient) CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error) {
	m.createdEvent = ev
	if m.createErr != nil {
		return "", m.createErr
	}
	return "https://calendar.google.com/event?eid=123", nil
}

func (m *mockCalClient) ListEvents(ctx context.Context, timeMin, timeMax string, q string) ([]*calendar.Event, error) {
	m.listQuery = q
	return []*calendar.Event{
		{Summary: "Team Sync"},
	}, nil
}

func (m *mockCalClient) UpdateEvent(ctx context.Context, eventID string, ev models.CalendarEvent) error {
	m.updatedEvent = ev
	return nil
}

func (m *mockCalClient) DeleteEvent(ctx context.Context, eventID string) error {
	m.deletedID = eventID
	return nil
}

func TestWorkspaceCalendar_Operations(t *testing.T) {
	mockClient := &mockCalClient{}
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 5)
	bus.SubscribeTopic("schedule", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	factory := func(ctx context.Context, userID string) (orchestrator.CalendarClient, error) {
		if userID == "unauth_user" {
			return nil, orchestrator.ErrCalendarNotConnected
		}
		if userID == "expired_user" {
			return nil, errors.New("invalid_grant")
		}
		return mockClient, nil
	}

	mod := NewModule(factory, WithEventBus(bus))
	if mod.Name() != "workspace_calendar" {
		t.Errorf("expected name workspace_calendar, got %s", mod.Name())
	}
	if len(mod.Declarations()) != 4 {
		t.Errorf("expected 4 declarations, got %d", len(mod.Declarations()))
	}

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_123",
			FamilyGroup: "family_1",
		},
		EventBus: bus,
	}

	// 1. Create Event
	res, err := mod.Execute(context.Background(), callCtx, "create_calendar_event", map[string]interface{}{
		"title":      "Doctor Appointment",
		"start_time": "2026-11-06T10:00:00Z",
		"end_time":   "2026-11-06T11:00:00Z",
		"timezone":   "Asia/Jerusalem",
	})
	if err != nil {
		t.Fatalf("create event failed: %v", err)
	}
	if mockClient.createdEvent.Title != "Doctor Appointment" {
		t.Errorf("expected title 'Doctor Appointment', got %q", mockClient.createdEvent.Title)
	}
	if res != "Event Created Successfully! Link: https://calendar.google.com/event?eid=123" {
		t.Errorf("unexpected create response: %s", res)
	}

	// Verify EventBus emission
	select {
	case ev := <-receivedEvents:
		if ev.Topic != "schedule" || ev.Action != "create" || ev.ActorUserID != "tg_123" || ev.FamilyGroup != "family_1" {
			t.Errorf("unexpected mutation event: %+v", ev)
		}
	default:
		t.Error("expected mutation event on create")
	}

	// 2. Update Event
	_, err = mod.Execute(context.Background(), callCtx, "update_calendar_event", map[string]interface{}{
		"id":         "evt_123",
		"title":      "Doctor Appointment (Rescheduled)",
		"start_time": "2026-11-06T11:00:00Z",
		"end_time":   "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("update event failed: %v", err)
	}
	if mockClient.updatedEvent.ID != "evt_123" {
		t.Errorf("expected event id 'evt_123', got %q", mockClient.updatedEvent.ID)
	}

	// 3. Delete Event
	_, err = mod.Execute(context.Background(), callCtx, "delete_calendar_event", map[string]interface{}{
		"id": "evt_123",
	})
	if err != nil {
		t.Fatalf("delete event failed: %v", err)
	}
	if mockClient.deletedID != "evt_123" {
		t.Errorf("expected deleted id 'evt_123', got %q", mockClient.deletedID)
	}

	// 4. List Events
	_, err = mod.Execute(context.Background(), callCtx, "list_calendar_events", map[string]interface{}{
		"query": "Team",
	})
	if err != nil {
		t.Fatalf("list events failed: %v", err)
	}
	if mockClient.listQuery != "Team" {
		t.Errorf("expected query 'Team', got %q", mockClient.listQuery)
	}

	// 5. Unauthorized User
	unauthCtx := orchestrator.CallContext{
		User: &models.User{PlatformID: "unauth_user"},
	}
	res, _ = mod.Execute(context.Background(), unauthCtx, "create_calendar_event", map[string]interface{}{
		"title": "Test",
	})
	if res != "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again." {
		t.Errorf("unexpected unauthorized response: %s", res)
	}
}
