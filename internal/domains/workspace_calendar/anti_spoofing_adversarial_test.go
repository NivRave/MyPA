package workspace_calendar

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"
)

type recordingCalClient struct {
	createdEvent models.CalendarEvent
	updatedID    string
	deletedID    string
}

func (r *recordingCalClient) CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error) {
	r.createdEvent = ev
	return "https://calendar.google.com/event?eid=test_eid", nil
}
func (r *recordingCalClient) ListEvents(ctx context.Context, timeMin, timeMax string, q string) ([]*calendar.Event, error) {
	return []*calendar.Event{{Summary: "Sync"}}, nil
}
func (r *recordingCalClient) UpdateEvent(ctx context.Context, eventID string, ev models.CalendarEvent) error {
	r.updatedID = eventID
	return nil
}
func (r *recordingCalClient) DeleteEvent(ctx context.Context, eventID string) error {
	r.deletedID = eventID
	return nil
}

// TestChallenge2_WorkspaceCalendar_AntiSpoofing verifies that workspace_calendar strictly
// resolves the calendar client via callCtx.ActorID() and publishes events scoped to
// callCtx.TenantFamilyGroup(), ignoring any spoofed parameters in args.
func TestChallenge2_WorkspaceCalendar_AntiSpoofing(t *testing.T) {
	mockClient := &recordingCalClient{}
	var factoryReceivedUser string

	factory := func(ctx context.Context, userID string) (orchestrator.CalendarClient, error) {
		factoryReceivedUser = userID
		return mockClient, nil
	}

	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 10)
	bus.SubscribeTopic("schedule", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(factory, WithEventBus(bus))

	legitUser := &models.User{
		ID:          "uuid-victim",
		PlatformID:  "cal_victim_platform",
		FamilyGroup: "cal_family_legit",
	}
	callCtx := orchestrator.CallContext{
		User:     legitUser,
		Message:  models.Message{UserID: "cal_victim_platform"},
		EventBus: bus,
	}

	adversarialArgs := map[string]interface{}{
		"id":           "event-456",
		"title":        "Important Meeting",
		"start_time":   "2026-11-10T10:00:00Z",
		"end_time":     "2026-11-10T11:00:00Z",
		"user_id":      "spoofed_attacker_999",
		"family_group": "evil_family",
	}

	t.Run("create_calendar_event uses canonical ActorID and TenantFamilyGroup", func(t *testing.T) {
		factoryReceivedUser = ""
		res, err := mod.Execute(context.Background(), callCtx, "create_calendar_event", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "Event Created Successfully")
		assert.Equal(t, "cal_victim_platform", factoryReceivedUser, "Factory must receive canonical ActorID()")
		assert.NotEqual(t, "spoofed_attacker_999", factoryReceivedUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "cal_victim_platform", ev.ActorUserID)
			assert.Equal(t, "cal_family_legit", ev.FamilyGroup)
			assert.NotEqual(t, "spoofed_attacker_999", ev.ActorUserID)
			assert.NotEqual(t, "evil_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for create_calendar_event")
		}
	})

	t.Run("update_calendar_event uses canonical ActorID and TenantFamilyGroup", func(t *testing.T) {
		factoryReceivedUser = ""
		res, err := mod.Execute(context.Background(), callCtx, "update_calendar_event", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "Event Updated Successfully")
		assert.Equal(t, "cal_victim_platform", factoryReceivedUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "cal_victim_platform", ev.ActorUserID)
			assert.Equal(t, "cal_family_legit", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for update_calendar_event")
		}
	})

	t.Run("delete_calendar_event uses canonical ActorID and TenantFamilyGroup", func(t *testing.T) {
		factoryReceivedUser = ""
		res, err := mod.Execute(context.Background(), callCtx, "delete_calendar_event", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "Event Deleted Successfully")
		assert.Equal(t, "cal_victim_platform", factoryReceivedUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "cal_victim_platform", ev.ActorUserID)
			assert.Equal(t, "cal_family_legit", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for delete_calendar_event")
		}
	})
}
