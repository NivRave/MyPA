package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// quietPerson stores a member of a household in zone with quiet hours (empty for none) and the
// audit session the engine reaches them through.
func quietPerson(t *testing.T, client *db.Client, id, label, zone, start, end string) models.User {
	t.Helper()
	u := householdtest.Member(t, client, id, id, "admin", label, "telegram")
	require.NoError(t, client.DB.Model(&models.Household{}).Where("id = ?", u.HouseholdID).
		Update("time_zone", zone).Error)
	if start != "" {
		require.NoError(t, client.UpsertUserPreference(&models.UserPreference{
			UserID: id, QuietHoursStart: start, QuietHoursEnd: end,
		}))
	}
	require.NoError(t, client.DB.Create(&models.AuditSession{
		ID: uuid.NewString(), UserID: id, ChatID: "chat-" + id, Source: "telegram",
		StartTime: time.Now(), Status: "success",
	}).Error)
	return u
}

// settableClock is the engine's clock in these tests.
type settableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *settableClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *settableClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func sentTexts(spy *spyTelegramClient) []string {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	return append([]string(nil), spy.sentMessages...)
}

func countContaining(msgs []string, sub string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// V4-05 done-when: "a non-urgent alert due in quiet hours is deferred". Dana's household is in
// Jerusalem and her quiet hours are 22:00-07:00. At 23:30 her time, her non-urgent reminder and a
// shared reminder assigned to her stay unsent; her urgent one goes out. At the same instant it is
// 16:30 for Noam in New York, outside his identical quiet hours, so his goes out: the window is
// read on each person's own clock. At 07:00 her held reminders go out, once.
func TestQuietHours_NonUrgentReminderDueInQuietHoursIsDeferred(t *testing.T) {
	client := newTestDBClient(t)
	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)

	dana := quietPerson(t, client, "quiet-dana", "QuietJerusalem", "Asia/Jerusalem", "22:00", "07:00")
	quietPerson(t, client, "quiet-noam", "QuietNewYork", "America/New_York", "22:00", "07:00")

	due := time.Now().Add(-time.Minute)
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "quiet-dana", Message: "Take the bins out", DueTime: due}))
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "quiet-dana", Message: "Take the night pill", DueTime: due, Urgent: true}))
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "quiet-noam", Message: "Call the plumber", DueTime: due}))
	scope := householdtest.Scope(t, client, "QuietJerusalem", dana.PlatformID, "admin")
	require.NoError(t, client.CreateSharedReminder(scope, &models.SharedTaskReminder{
		Topic: "family", Title: "Buy milk", DueTime: &due, CreatedByUserID: "quiet-dana", AssignedToUserID: "quiet-dana",
	}))

	spy := &spyTelegramClient{}
	clock := &settableClock{t: time.Date(2026, 11, 20, 23, 30, 0, 0, jerusalem)}
	engine := orchestrator.NewEngine(nil, nil, client, nil, adapterstest.Registry(spy, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithQuietHours(client).WithClock(clock.now)

	engine.CheckAndSendReminders()
	night := sentTexts(spy)
	assert.Equal(t, 1, countContaining(night, "Take the night pill"), "an urgent reminder is not held")
	assert.Equal(t, 1, countContaining(night, "Call the plumber"), "16:30 in New York is outside Noam's quiet hours")
	assert.Zero(t, countContaining(night, "Take the bins out"), "a non-urgent reminder waits for the end of quiet hours")
	assert.Zero(t, countContaining(night, "Buy milk"), "so does a shared reminder assigned to her")
	require.Len(t, night, 2)

	var held models.ScheduledReminder
	require.NoError(t, client.DB.Where("message = ?", "Take the bins out").First(&held).Error)
	assert.False(t, held.IsSent, "a held reminder stays due, so it cannot be lost")

	// Still quiet a minute later: nothing new, nothing twice.
	clock.set(time.Date(2026, 11, 20, 23, 31, 0, 0, jerusalem))
	engine.CheckAndSendReminders()
	require.Len(t, sentTexts(spy), 2)

	// 07:00 in Jerusalem: her quiet hours are over and both held reminders go out.
	clock.set(time.Date(2026, 11, 21, 7, 0, 0, 0, jerusalem))
	engine.CheckAndSendReminders()
	morning := sentTexts(spy)
	require.Len(t, morning, 4)
	assert.Equal(t, 1, countContaining(morning, "Take the bins out"))
	for _, m := range morning[2:] {
		assert.True(t, strings.HasPrefix(m, "chat-quiet-dana:"), "the held reminders go to Dana: %s", m)
	}
	assert.Equal(t, 1, countContaining(morning, "Buy milk"))
	require.NoError(t, client.DB.Where("message = ?", "Take the bins out").First(&held).Error)
	assert.True(t, held.IsSent)

	engine.CheckAndSendReminders()
	assert.Len(t, sentTexts(spy), 4, "a delivered reminder is not sent again")
}

// Without the quiet-hours store (a test or tool that does not wire it), reminders go out as they
// did before V4-05.
func TestQuietHours_NotWiredDeliversAsBefore(t *testing.T) {
	client := newTestDBClient(t)
	quietPerson(t, client, "quiet-unwired", "QuietUnwired", "UTC", "00:00", "23:59")
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "quiet-unwired", Message: "Water the plants", DueTime: time.Now().Add(-time.Minute)}))

	spy := &spyTelegramClient{}
	engine := orchestrator.NewEngine(nil, nil, client, nil, adapterstest.Registry(spy, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithClock(func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) })
	engine.CheckAndSendReminders()
	assert.Equal(t, 1, countContaining(sentTexts(spy), "Water the plants"))
}

// The morning briefing follows each person's clock: 08:00 in their household's zone, or the end
// of their quiet hours when 08:00 falls inside them. It used to go out at 08:00 in one zone for
// the whole install.
func TestQuietHours_BriefingFollowsEachPersonsClock(t *testing.T) {
	client := newTestDBClient(t)
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl)
	model.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return(nil, errors.New("no embeddings in this test")).AnyTimes()
	model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "Here is your morning briefing."}, nil).AnyTimes()

	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	quietPerson(t, client, "brief-dana", "BriefJerusalem", "Asia/Jerusalem", "23:00", "09:30")
	quietPerson(t, client, "brief-noam", "BriefNewYork", "America/New_York", "", "")

	spy := &spyTelegramClient{}
	clock := &settableClock{}
	engine := orchestrator.NewEngine(nil, store, client, model, adapterstest.Registry(spy, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithQuietHours(client).WithClock(clock.now)
	briefings := func(at time.Time) []string {
		clock.set(at)
		started := engine.SendDueBriefings(context.Background())
		engine.Wait()
		return started
	}

	assert.Empty(t, briefings(time.Date(2026, 11, 20, 8, 0, 0, 0, jerusalem)),
		"08:00 in Jerusalem is inside Dana's quiet hours, and 01:00 in New York")
	assert.Equal(t, []string{"brief-dana"}, briefings(time.Date(2026, 11, 20, 9, 30, 0, 0, jerusalem)),
		"Dana's briefing comes when her quiet hours end")
	assert.Equal(t, []string{"brief-noam"}, briefings(time.Date(2026, 11, 20, 8, 0, 0, 0, newYork)),
		"Noam's comes at 08:00 in New York")
	assert.Empty(t, briefings(time.Date(2026, 11, 20, 8, 1, 0, 0, newYork)), "once a morning")

	sent := sentTexts(spy)
	assert.Equal(t, 1, countContaining(sent, "chat-brief-dana:Here is your morning briefing."))
	assert.Equal(t, 1, countContaining(sent, "chat-brief-noam:Here is your morning briefing."))
}
