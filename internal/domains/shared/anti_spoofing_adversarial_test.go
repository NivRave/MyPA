package shared

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDBForReminders struct {
	orchestrator.DBClient
	savedReminder models.ScheduledReminder
}

func (m *mockDBForReminders) SaveReminder(r models.ScheduledReminder) error {
	m.savedReminder = r
	return nil
}

// TestShared_AntiSpoofing verifies that the shared module strictly uses
// callCtx.ActorID() and callCtx.TenantFamilyGroup() when creating shared spaces,
// shared task reminders, and personal scheduled reminders, ignoring foreign spoofed args.
func TestShared_AntiSpoofing(t *testing.T) {
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 20)
	bus.SubscribeTopic("shared", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})
	bus.SubscribeTopic("family", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(repo, bus, nil)

	legitUser := &models.User{
		ID:          "uuid-victim",
		PlatformID:  "shared_victim_actor",
		FamilyGroup: "shared_legit_family",
	}
	dbMock := &mockDBForReminders{}
	callCtx := orchestrator.CallContext{
		User:     legitUser,
		Message:  models.Message{UserID: "shared_victim_actor"},
		DB:       dbMock,
		EventBus: bus,
	}

	adversarialArgs := map[string]interface{}{
		"topic":               "family",
		"title":               "Clean the kitchen",
		"message":             "Buy milk and eggs",
		"due_time":            "2026-11-10T15:00:00Z",
		"user_id":             "hacker_attacker_666",
		"family_group":        "evil_family_hacker",
		"owner_user_id":       "hacker_attacker_666",
		"created_by_user_id":  "hacker_attacker_666",
		"assigned_to_user_id": "partner_user",
	}

	t.Run("configure_shared_topic ignores spoofed owner and family_group", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "configure_shared_topic", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "shared_legit_family")

		spaces, err := repo.GetSharedSpacesForFamily("shared_legit_family")
		require.NoError(t, err)
		require.Len(t, spaces, 1)
		assert.Equal(t, "shared_victim_actor", spaces[0].OwnerUserID, "OwnerUserID must be canonical ActorID")
		assert.Equal(t, "shared_legit_family", spaces[0].FamilyGroup)

		// Verify no space was created for evil_family_hacker
		evilSpaces, err := repo.GetSharedSpacesForFamily("evil_family_hacker")
		require.NoError(t, err)
		assert.Empty(t, evilSpaces)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "shared_victim_actor", ev.ActorUserID)
			assert.Equal(t, "shared_legit_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for configure_shared_topic")
		}
	})

	t.Run("create_shared_reminder strictly scopes to TenantFamilyGroup and ActorID", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "create_shared_reminder", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "Created shared task/reminder")

		reminders, err := repo.ListSharedReminders("shared_legit_family", "", false)
		require.NoError(t, err)
		require.Len(t, reminders, 1)
		assert.Equal(t, "shared_victim_actor", reminders[0].CreatedByUserID, "CreatedByUserID must be canonical ActorID")
		assert.Equal(t, "shared_legit_family", reminders[0].FamilyGroup)

		// Verify evil family has no reminders
		evilReminders, err := repo.ListSharedReminders("evil_family_hacker", "", false)
		require.NoError(t, err)
		assert.Empty(t, evilReminders)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "shared_victim_actor", ev.ActorUserID)
			assert.Equal(t, "shared_legit_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for create_shared_reminder")
		}
	})

	t.Run("schedule_reminder saves with ActorID and TenantFamilyGroup", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "schedule_reminder", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "Reminder Set")

		assert.Equal(t, "shared_victim_actor", dbMock.savedReminder.UserID, "Scheduled reminder UserID must be canonical ActorID")
		assert.NotEqual(t, "hacker_attacker_666", dbMock.savedReminder.UserID)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "shared_victim_actor", ev.ActorUserID)
			assert.Equal(t, "shared_legit_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for schedule_reminder")
		}
	})
}
