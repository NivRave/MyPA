package shared

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-05: the reminder tools store the urgent flag the model passes, and a reminder is not urgent
// unless it says so. Only urgent reminders reach their person during quiet hours.
func TestReminderTools_StoreTheUrgentFlag(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("urgent_family", []models.User{{ID: "uuid-urgent", PlatformID: "urgent_actor", FamilyGroup: "urgent_family"}})
	mod := NewModule(repo, nil, nil)
	dbMock := &mockDBForReminders{}
	callCtx := orchestrator.CallContext{
		User:    &models.User{ID: "uuid-urgent", PlatformID: "urgent_actor", FamilyGroup: "urgent_family"},
		Scope:   sc(t, "urgent_family", "urgent_actor"),
		Message: channel.Inbound{Sender: "urgent_actor"},
		DB:      dbMock,
	}
	ctx := context.Background()

	_, err := mod.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
		"message": "Night pill", "due_time": "2026-11-20T21:00:00Z", "urgent": true,
	})
	require.NoError(t, err)
	assert.True(t, dbMock.savedReminder.Urgent)

	_, err = mod.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
		"message": "Bins", "due_time": "2026-11-20T21:00:00Z",
	})
	require.NoError(t, err)
	assert.False(t, dbMock.savedReminder.Urgent, "not urgent unless the model says so")

	for _, urgent := range []bool{true, false} {
		_, err = mod.Execute(ctx, callCtx, "create_shared_reminder", map[string]interface{}{
			"title": "Check the heater", "due_time": "2026-11-20T21:00:00Z", "urgent": urgent,
		})
		require.NoError(t, err)
	}
	got, err := repo.ListSharedReminders(sc(t, "urgent_family", "urgent_actor"), "", false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	flags := []bool{got[0].Urgent, got[1].Urgent}
	assert.ElementsMatch(t, []bool{true, false}, flags)
}
