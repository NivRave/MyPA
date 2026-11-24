package schedule_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-43: the calendar tools store a false as asked: an event kept out of Google, and a topic
// left out of the morning briefing (on create, and on update from true back to false).
func TestChat_CalendarStoresAFalseAsAsked(t *testing.T) {
	c, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	u := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	s, err := identity.ForUser(ctx, c, &u)
	require.NoError(t, err)
	call := orchestrator.CallContext{User: &u, Scope: s, DB: c, Extra: map[string]interface{}{}}
	mod := schedule.NewModule(c)

	_, err = mod.Execute(ctx, call, "create_calendar_event", map[string]interface{}{
		"title": "Checkup BL43", "topic": "health", "start_time": "2026-12-01T10:00:00Z", "sync_to_google": false})
	require.NoError(t, err)
	var ev models.ScheduleEvent
	require.NoError(t, c.DB.Where("household_id = ? AND title = ?", u.HouseholdID, "Checkup BL43").First(&ev).Error)
	assert.False(t, ev.SyncToGoogle, "kept out of Google")

	for _, want := range []bool{false, true, false} {
		_, err = mod.Execute(ctx, call, "configure_topic_calendar", map[string]interface{}{
			"topic": "health", "include_in_morning_briefing": want})
		require.NoError(t, err)
		var cfgs []models.TopicCalendarConfig
		require.NoError(t, c.DB.Where("user_id = ? AND topic = ?", u.PlatformID, "health").Find(&cfgs).Error)
		require.Len(t, cfgs, 1, "one config, created then updated")
		assert.Equal(t, want, cfgs[0].IncludeInMorningBriefing)
	}
}
