package shared_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-69 (the owner's decision): /undo right after "remind me at 7" cancels the reminder. Once it
// has gone out, /undo says so and changes nothing.
func TestUndo_AReminderToOneself(t *testing.T) {
	c, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	bus := events.NewEventBus()
	t.Cleanup(func() { _ = bus.Close() })
	dur := durability.NewModule(c, bus)
	levi := "Levi-" + uuid.NewString()
	u := householdtest.Member(t, c, "1001-"+levi, "Dana", "family", levi, "telegram")
	s, err := identity.ForUser(ctx, c, &u)
	require.NoError(t, err)
	call := orchestrator.CallContext{User: &u, Scope: s, DB: c, EventBus: bus, Extra: map[string]interface{}{}}
	mod := shared.NewModule(c, bus, nil)

	remind := func(message string) models.ScheduledReminder {
		t.Helper()
		_, err := mod.Execute(ctx, call, "schedule_reminder", map[string]interface{}{
			"message": message, "due_time": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)})
		require.NoError(t, err)
		var r models.ScheduledReminder
		require.NoError(t, c.DB.Where("user_id = ? AND message = ?", u.PlatformID, message).First(&r).Error)
		return r
	}
	count := func(id uint) int64 {
		var n int64
		require.NoError(t, c.DB.Model(&models.ScheduledReminder{}).Where("id = ?", id).Count(&n).Error)
		return n
	}

	t.Run("before it fires, /undo cancels it", func(t *testing.T) {
		r := remind("Call the plumber BL69")
		res, err := dur.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.False(t, res.NotUndoable, res.Detail)
		assert.Zero(t, count(r.ID), "the reminder is gone")
	})

	// The reminder poller claims a reminder before it hands it over (db.Client.ClaimReminder), and
	// the claim and the undo lock the same row: whichever comes first wins, never both.
	t.Run("once claimed for sending, /undo says it went out", func(t *testing.T) {
		r := remind("Feed the cat BL69")
		claimed, err := c.ClaimReminder(r.ID)
		require.NoError(t, err)
		require.True(t, claimed)
		res, err := dur.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.True(t, res.NotUndoable)
		assert.Contains(t, res.Detail, "already")
		assert.Equal(t, int64(1), count(r.ID))
	})

	t.Run("once undone, it is never claimed for sending", func(t *testing.T) {
		r := remind("Water the plants BL69")
		res, err := dur.RollbackLast(ctx, s)
		require.NoError(t, err)
		require.False(t, res.NotUndoable, res.Detail)
		claimed, err := c.ClaimReminder(r.ID)
		require.NoError(t, err)
		assert.False(t, claimed, "the poller finds nothing to send")
	})

	t.Run("a claim released after a failed hand-over leaves it due", func(t *testing.T) {
		r := remind("Pay the nanny BL69")
		claimed, err := c.ClaimReminder(r.ID)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, c.ReleaseReminder(r.ID))
		var got models.ScheduledReminder
		require.NoError(t, c.DB.First(&got, r.ID).Error)
		assert.False(t, got.IsSent)
		res, err := dur.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.False(t, res.NotUndoable, "still not sent: /undo cancels it")
		assert.Zero(t, count(r.ID))
	})

	t.Run("after it fired, /undo says so and changes nothing", func(t *testing.T) {
		r := remind("Take the bins out BL69")
		require.NoError(t, c.MarkReminderSent(r.ID))
		res, err := dur.RollbackLast(ctx, s)
		require.NoError(t, err)
		assert.True(t, res.NotUndoable, "nothing to cancel")
		assert.Contains(t, res.Detail, "already")
		assert.Contains(t, res.Detail, "Nothing was changed")
		assert.Equal(t, int64(1), count(r.ID), "the reminder that went out stays")
	})
}
