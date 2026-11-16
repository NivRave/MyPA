package schedule

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"
)

// BL-1: Google Calendar failures must never be silent. The tool result tells the user (so the model
// does not present local-only data as the full picture) and the operator gets a WARN log.

type failingCalendarClient struct{ err error }

func (f failingCalendarClient) CreateEvent(context.Context, models.CalendarEvent) (string, error) {
	return "", f.err
}
func (f failingCalendarClient) ListEvents(context.Context, string, string, string) ([]*calendar.Event, error) {
	return nil, f.err
}
func (f failingCalendarClient) UpdateEvent(context.Context, string, models.CalendarEvent) error {
	return f.err
}
func (f failingCalendarClient) DeleteEvent(context.Context, string) error { return f.err }

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func calendarFactory(c orchestrator.CalendarClient, err error) Option {
	return WithCalendarFactory(func(context.Context, string) (orchestrator.CalendarClient, error) {
		return c, err
	})
}

var aliceCtx = orchestrator.CallContext{User: &models.User{PlatformID: "usr_1"}}

func seedLocalEvent(t *testing.T, repo *fakeScheduleRepo, googleID string) {
	t.Helper()
	now := time.Now().Truncate(time.Hour)
	require.NoError(t, repo.CreateScheduleEvent(&models.ScheduleEvent{
		UserID: "usr_1", Topic: "baby", Title: "Morning Feeding",
		StartTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour),
		SyncToGoogle: googleID != "", ExternalGoogleEventID: googleID,
	}))
}

func TestListTopicSchedule_GoogleFailureIsReportedAndLogged(t *testing.T) {
	cases := map[string]Option{
		"client cannot be built": calendarFactory(nil, errors.New("decrypt oauth token: cipher: message authentication failed")),
		"list call fails":        calendarFactory(failingCalendarClient{err: errors.New("googleapi: 503")}, nil),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			repo := newFakeScheduleRepo()
			seedLocalEvent(t, repo, "")
			res, err := NewModule(repo, opt).Execute(context.Background(), aliceCtx, "list_topic_schedule", map[string]interface{}{})
			require.NoError(t, err)
			assert.Contains(t, res, "Morning Feeding", "local events are still listed")
			assert.Contains(t, res, "Google Calendar could not be reached")
			assert.Contains(t, logs.String(), `"level":"WARN"`)
			assert.Contains(t, logs.String(), `"op":"list"`)
			assert.NotContains(t, logs.String(), "Morning Feeding", "no event content in logs (P3-3)")
		})
	}
}

// The misleading case from the first deploy: nothing local + Google failing must not read as "free day".
func TestListTopicSchedule_GoogleFailureWithNoLocalEvents(t *testing.T) {
	captureLogs(t)
	res, err := NewModule(newFakeScheduleRepo(), calendarFactory(nil, errors.New("boom"))).
		Execute(context.Background(), aliceCtx, "list_topic_schedule", map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, res, "No scheduled events found")
	assert.Contains(t, res, "Google Calendar could not be reached")
}

func TestListTopicSchedule_NotConnectedIsExplainedWithoutWarning(t *testing.T) {
	logs := captureLogs(t)
	repo := newFakeScheduleRepo()
	seedLocalEvent(t, repo, "")
	res, err := NewModule(repo, calendarFactory(nil, orchestrator.ErrCalendarNotConnected)).
		Execute(context.Background(), aliceCtx, "list_topic_schedule", map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, res, "Morning Feeding")
	assert.Contains(t, res, "Google Calendar is not connected")
	assert.Contains(t, res, "/connect")
	assert.NotContains(t, logs.String(), `"level":"WARN"`, "not connected is a normal state, not an incident")
}

func TestListTopicSchedule_NoCalendarIntegrationStaysSilent(t *testing.T) {
	repo := newFakeScheduleRepo()
	seedLocalEvent(t, repo, "")
	res, err := NewModule(repo).Execute(context.Background(), aliceCtx, "list_topic_schedule", map[string]interface{}{})
	require.NoError(t, err)
	assert.NotContains(t, res, "⚠️")
}

func TestScheduleTopicEvent_GoogleCreateFailureIsNotReportedAsSynced(t *testing.T) {
	logs := captureLogs(t)
	repo := newFakeScheduleRepo()
	res, err := NewModule(repo, calendarFactory(failingCalendarClient{err: errors.New("googleapi: 403")}, nil)).
		Execute(context.Background(), aliceCtx, "schedule_topic_event", map[string]interface{}{
			"title": "Vaccination", "topic": "baby", "start_time": "2026-11-20T09:00:00Z", "sync_to_google": true,
		})
	require.NoError(t, err)
	assert.NotContains(t, res, "Synced ✅")
	assert.Contains(t, res, "**Google Calendar**: ⚠️ Sync failed")
	ev, err := repo.GetScheduleEvent(1)
	require.NoError(t, err)
	require.NotNil(t, ev, "the event is still saved locally")
	assert.Empty(t, ev.ExternalGoogleEventID)
	assert.Contains(t, logs.String(), `"op":"create"`)
	assert.NotContains(t, logs.String(), "Vaccination")
}

func TestScheduleTopicEvent_NotConnectedIsStated(t *testing.T) {
	res, err := NewModule(newFakeScheduleRepo(), calendarFactory(nil, orchestrator.ErrCalendarNotConnected)).
		Execute(context.Background(), aliceCtx, "schedule_topic_event", map[string]interface{}{
			"title": "Vaccination", "topic": "baby", "start_time": "2026-11-20T09:00:00Z", "sync_to_google": true,
		})
	require.NoError(t, err)
	assert.Contains(t, res, "**Google Calendar**: Not connected")
	assert.Contains(t, res, "/connect")
}

func TestScheduleTopicEvent_PartnerSyncFailureIsReported(t *testing.T) {
	logs := captureLogs(t)
	userCal := newFakeCalendarClient()
	mod := NewModule(newFakeScheduleRepo(), WithCalendarFactory(func(_ context.Context, uid string) (orchestrator.CalendarClient, error) {
		if uid == "tg_alice" {
			return userCal, nil
		}
		return failingCalendarClient{err: errors.New("googleapi: 500")}, nil
	}))
	callCtx := orchestrator.CallContext{
		User:  &models.User{PlatformID: "tg_alice", FamilyGroup: "Smiths"},
		Extra: map[string]interface{}{"partner_user_id": "tg_bob"},
	}
	res, err := mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title": "Boiler Inspection", "topic": "house", "start_time": "2026-11-06T09:00:00Z",
		"sync_to_google": true, "is_shared": true,
	})
	require.NoError(t, err)
	assert.Contains(t, res, "**Google Calendar**: Synced ✅")
	assert.Contains(t, res, "**Partner Calendar**: ⚠️ Sync failed")
	assert.Contains(t, logs.String(), `"op":"create_partner"`)
}

func TestDeleteTopicEvent_GoogleDeleteFailureIsReported(t *testing.T) {
	logs := captureLogs(t)
	repo := newFakeScheduleRepo()
	seedLocalEvent(t, repo, "gcal_1")
	res, err := NewModule(repo, calendarFactory(failingCalendarClient{err: errors.New("googleapi: 500")}, nil)).
		Execute(context.Background(), aliceCtx, "delete_topic_event", map[string]interface{}{"event_id": 1})
	require.NoError(t, err)
	assert.Contains(t, res, "successfully deleted")
	assert.Contains(t, res, "could not be removed from Google Calendar")
	assert.Contains(t, logs.String(), `"op":"delete"`)
}

func TestUpdateTopicEvent_GoogleUpdateFailureIsReported(t *testing.T) {
	logs := captureLogs(t)
	repo := newFakeScheduleRepo()
	seedLocalEvent(t, repo, "gcal_1")
	res, err := NewModule(repo, calendarFactory(nil, errors.New("decrypt failed"))).
		Execute(context.Background(), aliceCtx, "update_topic_event", map[string]interface{}{"event_id": 1, "title": "Late Feeding"})
	require.NoError(t, err)
	assert.Contains(t, res, "Late Feeding")
	assert.Contains(t, res, "was not updated in Google Calendar")
	assert.Contains(t, logs.String(), `"op":"update"`)
}
