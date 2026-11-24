package shared

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jerusalemCall is a call from a person whose household is in Asia/Jerusalem (UTC+2 in November).
func jerusalemCall(t *testing.T) (orchestrator.CallContext, *mockDBForReminders, *inMemorySharedRepo) {
	t.Helper()
	const label = "bl50_jerusalem"
	h := households.Household(label)
	h.TimeZone = "Asia/Jerusalem"
	households.Put(h)
	repo := newInMemoryRepo()
	repo.setUsers(label, []models.User{{ID: "uuid-bl50", PlatformID: "bl50_actor", FamilyGroup: label}})
	dbMock := &mockDBForReminders{}
	return orchestrator.CallContext{
		User:    &models.User{ID: "uuid-bl50", PlatformID: "bl50_actor", FamilyGroup: label, HouseholdID: h.ID},
		Scope:   sc(t, label, "bl50_actor"),
		Message: channel.Inbound{Sender: "bl50_actor"},
		DB:      dbMock,
	}, dbMock, repo
}

// BL-50: a due time without an offset is read in the person's time zone, and the confirmation
// shows it in that zone. A time with an offset is the instant it names, as before.
func TestScheduleReminder_ANaiveDueTimeIsThePersonsLocalTime(t *testing.T) {
	callCtx, dbMock, repo := jerusalemCall(t)
	mod := NewModule(repo, nil, nil)
	ctx := context.Background()

	res, err := mod.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
		"message": "Lock the door", "due_time": "2026-11-20T23:00:00",
	})
	require.NoError(t, err)
	assert.True(t, dbMock.savedReminder.DueTime.Equal(time.Date(2026, 11, 20, 21, 0, 0, 0, time.UTC)),
		"23:00 in Jerusalem is 21:00 UTC, got %s", dbMock.savedReminder.DueTime.UTC())
	assert.Contains(t, res, "23:00", "confirmed on the person's clock")

	res, err = mod.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
		"message": "Call New York", "due_time": "2026-11-20T23:00:00Z",
	})
	require.NoError(t, err)
	assert.True(t, dbMock.savedReminder.DueTime.Equal(time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)),
		"a time with an offset is unchanged, got %s", dbMock.savedReminder.DueTime.UTC())
	assert.Contains(t, res, "01:00", "and confirmed on the person's clock (01:00 the next day)")
}

func TestCreateSharedReminder_ANaiveDueTimeIsThePersonsLocalTime(t *testing.T) {
	callCtx, _, repo := jerusalemCall(t)
	mod := NewModule(repo, nil, nil)
	ctx := context.Background()

	res, err := mod.Execute(ctx, callCtx, "create_shared_reminder", map[string]interface{}{
		"title": "Pick up Noa", "due_time": "2026-11-20T23:00:00",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "2026-11-20 23:00")

	res2, err := mod.Execute(ctx, callCtx, "create_shared_reminder", map[string]interface{}{
		"title": "Call New York", "due_time": "2026-11-20T23:00:00+00:00",
	})
	require.NoError(t, err)
	assert.Contains(t, res2, "2026-11-21 01:00", "an offset is kept; the confirmation is on the person's clock")

	got, err := repo.ListSharedReminders(callCtx.Scope, "", false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	want := map[string]time.Time{
		"Pick up Noa":   time.Date(2026, 11, 20, 21, 0, 0, 0, time.UTC),
		"Call New York": time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC),
	}
	for _, r := range got {
		require.NotNil(t, r.DueTime, r.Title)
		assert.True(t, r.DueTime.Equal(want[r.Title]), "%s: got %s", r.Title, r.DueTime.UTC())
	}
}

// Without a household zone the install default the engine passes is used.
func TestScheduleReminder_FallsBackToTheInstallZone(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("bl50_nozone", []models.User{{ID: "uuid-nz", PlatformID: "nz_actor", FamilyGroup: "bl50_nozone"}})
	dbMock := &mockDBForReminders{}
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	callCtx := orchestrator.CallContext{
		User:     &models.User{ID: "uuid-nz", PlatformID: "nz_actor", FamilyGroup: "bl50_nozone"},
		Scope:    sc(t, "bl50_nozone", "nz_actor"),
		Message:  channel.Inbound{Sender: "nz_actor"},
		DB:       dbMock,
		Location: ny,
	}
	_, err = NewModule(repo, nil, nil).Execute(context.Background(), callCtx, "schedule_reminder", map[string]interface{}{
		"message": "Bins", "due_time": "2026-11-20T23:00:00",
	})
	require.NoError(t, err)
	assert.True(t, dbMock.savedReminder.DueTime.Equal(time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)), "got %s", dbMock.savedReminder.DueTime.UTC())
}
