package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/quiet"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuietSettings_ReadsTheHouseholdZoneAndThePersonsQuietHours(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	u := householdtest.User(t, client, "quiet-dana", "Dana", "admin", "QuietFamily")
	require.NoError(t, client.DB.Model(&models.Household{}).Where("id = ?", u.HouseholdID).
		Update("time_zone", "Asia/Jerusalem").Error)
	require.NoError(t, client.UpsertUserPreference(&models.UserPreference{
		UserID: "quiet-dana", QuietHoursStart: "22:00", QuietHoursEnd: "07:00",
	}))

	s, err := client.QuietSettings(ctx, "quiet-dana")
	require.NoError(t, err)
	assert.Equal(t, quiet.Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, s)

	// A member who never set quiet hours: the household's zone, no window.
	householdtest.User(t, client, "quiet-omer", "Omer", "family", "QuietFamily")
	s, err = client.QuietSettings(ctx, "quiet-omer")
	require.NoError(t, err)
	assert.Equal(t, quiet.Settings{TimeZone: "Asia/Jerusalem"}, s)

	// Nobody behind the key: empty settings, so the install zone and no quiet hours apply.
	s, err = client.QuietSettings(ctx, "quiet-nobody")
	require.NoError(t, err)
	assert.Equal(t, quiet.Settings{}, s)
}

// Both values of the urgent flag survive a write and a read (BL-43 is about a false that GORM
// drops when the column's default is true; this default is false, and both directions are checked).
func TestReminders_UrgentFlagIsStoredBothWays(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	due := time.Now().Add(time.Hour)
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "urgent-u", Message: "pill", DueTime: due, Urgent: true}))
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "urgent-u", Message: "bins", DueTime: due}))
	var got []models.ScheduledReminder
	require.NoError(t, client.DB.Where("user_id = ?", "urgent-u").Order("id").Find(&got).Error)
	require.Len(t, got, 2)
	assert.True(t, got[0].Urgent)
	assert.False(t, got[1].Urgent)

	scope := householdtest.Scope(t, client, "UrgentFamily", "urgent-u", "admin")
	for _, urgent := range []bool{true, false} {
		r := models.SharedTaskReminder{Topic: "family", Title: "t", DueTime: &due, CreatedByUserID: "urgent-u", Urgent: urgent}
		require.NoError(t, client.CreateSharedReminder(scope, &r))
		var back models.SharedTaskReminder
		require.NoError(t, client.DB.First(&back, r.ID).Error)
		assert.Equal(t, urgent, back.Urgent)
	}
}
