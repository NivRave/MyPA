package schedule

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"
)

// V4-08: the calendar domain is one tool set. These tests carry over what the retired
// workspace_calendar module's tests proved (its four tools now run here) and cover the paths
// the merge added: Google-only events by their Google ID, the event's own time zone, and the
// sync flag.

// mutationRecorder subscribes to the schedule topic and keeps every event it sees.
func mutationRecorder(t *testing.T) (*events.EventBus, chan events.MutationEvent) {
	t.Helper()
	bus := events.NewEventBus()
	got := make(chan events.MutationEvent, 10)
	bus.SubscribeTopic("schedule", func(_ context.Context, ev events.MutationEvent) error {
		got <- ev
		return nil
	})
	return bus, got
}

func lastMutation(t *testing.T, got chan events.MutationEvent) events.MutationEvent {
	t.Helper()
	select {
	case ev := <-got:
		return ev
	default:
		t.Fatal("expected a MutationEvent")
		return events.MutationEvent{}
	}
}

func TestCalendarTools_TheModuleDeclaresTheFiveCalendarTools(t *testing.T) {
	var names []string
	for _, d := range NewModule(nil).Declarations() {
		names = append(names, d.Name)
	}
	assert.Equal(t, []string{
		"create_calendar_event", "list_calendar_events", "update_calendar_event",
		"delete_calendar_event", "configure_topic_calendar",
	}, names)
}

// A plain event (no topic) reaches Google with its own title, in the zone the model gave, and
// is kept in MyPA with the Google ID, so it can be changed and deleted later.
func TestCalendarTools_CreateWithoutTopicIsAPlainEventInItsZone(t *testing.T) {
	repo := newFakeScheduleRepo()
	cal := newFakeCalendarClient()
	bus, got := mutationRecorder(t)
	mod := NewModule(repo, calendarFactory(cal, nil), WithEventBus(bus))

	res, err := mod.Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
		"title":      "Doctor Appointment",
		"start_time": "2026-11-06T10:00:00",
		"end_time":   "2026-11-06T11:00:00",
		"timezone":   "Asia/Jerusalem",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Synced ✅")

	require.Len(t, cal.createdEvents, 1)
	sent := cal.createdEvents[0]
	assert.Equal(t, "Doctor Appointment", sent.Title, "an event without a topic gets no [Topic] prefix")
	assert.Equal(t, "Asia/Jerusalem", sent.Timezone)
	assert.Equal(t, "2026-11-06T10:00:00+02:00", sent.StartTime)
	assert.Equal(t, "2026-11-06T11:00:00+02:00", sent.EndTime)

	stored := repo.events[1]
	require.NotNil(t, stored)
	assert.Equal(t, "general", stored.Topic)
	assert.Equal(t, "gcal_evt_1", stored.ExternalGoogleEventID)
	assert.True(t, stored.StartTime.Equal(time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)), "10:00 in Jerusalem is 08:00 UTC, got %s", stored.StartTime)

	ev := lastMutation(t, got)
	assert.Equal(t, "create", ev.Action)
	assert.Equal(t, "ScheduleEvent", ev.EntityType, "the create is undoable: it is a MyPA row")
	assert.Equal(t, aliceCtx.Scope.HouseholdID(), ev.HouseholdID)
}

// A time with an offset keeps its instant; the zone only says how Google shows it.
func TestCalendarTools_CreateKeepsAnExplicitOffset(t *testing.T) {
	cal := newFakeCalendarClient()
	repo := newFakeScheduleRepo()
	_, err := NewModule(repo, calendarFactory(cal, nil)).Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
		"title": "Call", "start_time": "2026-11-06T10:00:00Z", "timezone": "Asia/Jerusalem",
	})
	require.NoError(t, err)
	require.Len(t, cal.createdEvents, 1)
	assert.Equal(t, "2026-11-06T12:00:00+02:00", cal.createdEvents[0].StartTime)
	assert.True(t, repo.events[1].StartTime.Equal(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)))
}

func TestCalendarTools_CreateRefusesAnUnknownZone(t *testing.T) {
	_, err := NewModule(newFakeScheduleRepo()).Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
		"title": "Call", "start_time": "2026-11-06T10:00:00", "timezone": "Mars/Olympus",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timezone")
}

// workspace_calendar took recurrence as a list of RRULE lines; the topic tool took one string.
// Both forms work.
func TestCalendarTools_CreateTakesRecurrenceAsAListOrAString(t *testing.T) {
	for name, rec := range map[string]interface{}{
		"list":   []interface{}{"RRULE:FREQ=WEEKLY;BYDAY=TU"},
		"string": "RRULE:FREQ=WEEKLY;BYDAY=TU",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScheduleRepo()
			cal := newFakeCalendarClient()
			_, err := NewModule(repo, calendarFactory(cal, nil)).Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
				"title": "Yoga", "start_time": "2026-11-10T18:00:00Z", "timezone": "UTC", "recurrence": rec,
			})
			require.NoError(t, err)
			require.Len(t, cal.createdEvents, 1)
			assert.Equal(t, []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"}, cal.createdEvents[0].Recurrence)
			assert.Equal(t, "RRULE:FREQ=WEEKLY;BYDAY=TU", repo.events[1].RecurrenceRule)
		})
	}
}

// sync_to_google is the flag that replaces the choice between the two tool sets.
func TestCalendarTools_SyncFlagKeepsAnEventOutOfGoogle(t *testing.T) {
	repo := newFakeScheduleRepo()
	cal := newFakeCalendarClient()
	res, err := NewModule(repo, calendarFactory(cal, nil)).Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
		"title": "Vitamin D", "topic": "health", "start_time": "2026-11-10T08:00:00Z", "timezone": "UTC", "sync_to_google": false,
	})
	require.NoError(t, err)
	assert.Empty(t, cal.createdEvents)
	assert.Contains(t, res, "Internal-only")
	assert.Empty(t, repo.events[1].ExternalGoogleEventID)
}

// The event created above can be changed and deleted in Google: the stored ID is the one
// Google returned, so the follow-up call targets the same event.
func TestCalendarTools_CreatedEventIsUpdatedAndDeletedInGoogle(t *testing.T) {
	repo := newFakeScheduleRepo()
	cal := newFakeCalendarClient()
	mod := NewModule(repo, calendarFactory(cal, nil))
	_, err := mod.Execute(context.Background(), aliceCtx, "create_calendar_event", map[string]interface{}{
		"title": "Dentist", "start_time": "2026-11-10T09:00:00Z", "timezone": "UTC",
	})
	require.NoError(t, err)

	_, err = mod.Execute(context.Background(), aliceCtx, "update_calendar_event", map[string]interface{}{
		"event_id": 1, "start_time": "2026-11-10T10:00:00", "timezone": "Asia/Jerusalem",
		"recurrence": []interface{}{"RRULE:FREQ=YEARLY"},
	})
	require.NoError(t, err)
	upd, ok := cal.updatedEvents["gcal_evt_1"]
	require.True(t, ok, "the update must reach the Google event the create made")
	assert.Equal(t, "Dentist", upd.Title, "a plain event stays unprefixed on update")
	assert.Equal(t, "2026-11-10T10:00:00+02:00", upd.StartTime)
	assert.Equal(t, "Asia/Jerusalem", upd.Timezone)
	assert.Equal(t, []string{"RRULE:FREQ=YEARLY"}, upd.Recurrence)
	assert.True(t, repo.events[1].StartTime.Equal(time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)))
	assert.Equal(t, "RRULE:FREQ=YEARLY", repo.events[1].RecurrenceRule)

	res, err := mod.Execute(context.Background(), aliceCtx, "delete_calendar_event", map[string]interface{}{"event_id": 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"gcal_evt_1"}, cal.deletedEvents)
	assert.NotContains(t, res, "⚠️")
}

// Rows written before V4-08 hold the event's web link instead of its ID. The ID is inside the
// link (eid = base64 of "<event id> <calendar>"), so update and delete still reach the event.
func TestCalendarTools_LegacyLinkRowsStillReachTheGoogleEvent(t *testing.T) {
	link := "https://www.google.com/calendar/event?eid=" + base64.RawURLEncoding.EncodeToString([]byte("abc123xyz usr@example.com"))
	assert.Equal(t, "abc123xyz", googleEventRef(link))
	assert.Equal(t, "gcal_evt_7", googleEventRef("gcal_evt_7"), "a plain ID is used as is")
	assert.Equal(t, "", googleEventRef("https://calendar.google.com/no-eid"), "a link without an ID is not sent to Google")

	repo := newFakeScheduleRepo()
	cal := newFakeCalendarClient()
	seedLocalEvent(t, repo, link)
	mod := NewModule(repo, calendarFactory(cal, nil))

	_, err := mod.Execute(context.Background(), aliceCtx, "update_calendar_event", map[string]interface{}{"event_id": 1, "title": "Late Feeding"})
	require.NoError(t, err)
	_, ok := cal.updatedEvents["abc123xyz"]
	assert.True(t, ok, "the update must use the ID from the stored link")

	_, err = mod.Execute(context.Background(), aliceCtx, "delete_calendar_event", map[string]interface{}{"event_id": 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"abc123xyz"}, cal.deletedEvents)
}

// Events made in Google itself are not MyPA rows. The list shows their Google ID, and update
// and delete take it, as workspace_calendar's tools did.
func TestCalendarTools_GoogleOnlyEventsByTheirGoogleID(t *testing.T) {
	cal := newFakeCalendarClient()
	cal.eventsToList = []*calendar.Event{{
		Id: "evt_123", Summary: "Team Sync",
		Start: &calendar.EventDateTime{DateTime: time.Now().Add(time.Hour).Format(time.RFC3339)},
		End:   &calendar.EventDateTime{DateTime: time.Now().Add(2 * time.Hour).Format(time.RFC3339)},
	}}
	bus, got := mutationRecorder(t)
	mod := NewModule(newFakeScheduleRepo(), calendarFactory(cal, nil), WithEventBus(bus))

	res, err := mod.Execute(context.Background(), aliceCtx, "list_calendar_events", map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, res, "Team Sync")
	assert.Contains(t, res, "**Google ID**: `evt_123`")

	res, err = mod.Execute(context.Background(), aliceCtx, "update_calendar_event", map[string]interface{}{
		"google_event_id": "evt_123", "title": "Doctor Appointment (Rescheduled)",
		"start_time": "2026-11-06T11:00:00Z", "end_time": "2026-11-06T12:00:00Z",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Event Updated Successfully")
	upd, ok := cal.updatedEvents["evt_123"]
	require.True(t, ok)
	assert.Equal(t, "Doctor Appointment (Rescheduled)", upd.Title)
	ev := lastMutation(t, got)
	assert.Equal(t, "update", ev.Action)
	assert.Equal(t, "CalendarEvent", ev.EntityType)
	assert.Equal(t, "evt_123", ev.EntityID)
	assert.Equal(t, aliceCtx.Scope.HouseholdID(), ev.HouseholdID)

	// The model may put a Google ID in event_id; a value that is not a number is one.
	res, err = mod.Execute(context.Background(), aliceCtx, "delete_calendar_event", map[string]interface{}{"event_id": "evt_123"})
	require.NoError(t, err)
	assert.Contains(t, res, "Event Deleted Successfully")
	assert.Equal(t, []string{"evt_123"}, cal.deletedEvents)
	ev = lastMutation(t, got)
	assert.Equal(t, "delete", ev.Action)
	assert.Equal(t, "evt_123", ev.EntityID)

	_, err = mod.Execute(context.Background(), aliceCtx, "delete_calendar_event", map[string]interface{}{})
	require.Error(t, err, "an update or delete needs one of the two IDs")
}

// The Google-only paths say why Google could not be used, as workspace_calendar did.
func TestCalendarTools_GoogleOnlyPathExplainsAMissingConnection(t *testing.T) {
	args := map[string]interface{}{"google_event_id": "evt_1"}
	cases := map[string]struct {
		opt  Option
		want string
	}{
		"not connected": {calendarFactory(nil, orchestrator.ErrCalendarNotConnected), "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again."},
		"expired":       {calendarFactory(nil, errors.New("invalid_grant")), "⚠️ Your Google connection expired or is invalid. Please send /connect again."},
		"not wired":     {func(*Module) {}, "⚠️ Google Calendar integration is not configured."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, tool := range []string{"update_calendar_event", "delete_calendar_event"} {
				res, err := NewModule(newFakeScheduleRepo(), c.opt).Execute(context.Background(), aliceCtx, tool, args)
				require.NoError(t, err)
				assert.Equal(t, c.want, res, tool)
			}
		})
	}
}

// list_calendar_events' free-text query (from workspace_calendar) reaches Google and filters
// MyPA's own events too.
func TestCalendarTools_ListQueryFiltersBothSources(t *testing.T) {
	repo := newFakeScheduleRepo()
	now := time.Now().Truncate(time.Hour)
	for _, title := range []string{"Morning Feeding", "Dentist checkup"} {
		require.NoError(t, repo.CreateScheduleEvent(aliceCtx.Scope, &models.ScheduleEvent{
			UserID: "usr_1", Topic: "health", Title: title, StartTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour),
		}))
	}
	cal := newFakeCalendarClient()
	res, err := NewModule(repo, calendarFactory(cal, nil)).Execute(context.Background(), aliceCtx, "list_calendar_events", map[string]interface{}{"query": "dentist"})
	require.NoError(t, err)
	assert.Equal(t, "dentist", cal.lastQuery)
	assert.Contains(t, res, "Dentist checkup")
	assert.NotContains(t, res, "Morning Feeding")
}

// Carried over from workspace_calendar's anti-spoofing test: the Google client and every
// MutationEvent use the caller's canonical identity, never IDs supplied in the arguments.
func TestCalendarTools_AntiSpoofing(t *testing.T) {
	cal := newFakeCalendarClient()
	var factoryUser string
	factory := WithCalendarFactory(func(_ context.Context, userID string) (orchestrator.CalendarClient, error) {
		factoryUser = userID
		return cal, nil
	})
	bus, got := mutationRecorder(t)
	mod := NewModule(newFakeScheduleRepo(), factory, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User:     &models.User{ID: "uuid-victim", PlatformID: "cal_victim_platform", FamilyGroup: "cal_family_legit"},
		Scope:    sc(t, "cal_family_legit", "cal_victim_platform"),
		Message:  channel.Inbound{Sender: "cal_victim_platform"},
		EventBus: bus,
	}
	spoofed := func(extra map[string]interface{}) map[string]interface{} {
		args := map[string]interface{}{
			"title": "Important Meeting", "start_time": "2026-11-10T10:00:00Z", "end_time": "2026-11-10T11:00:00Z",
			"timezone": "UTC", "user_id": "spoofed_attacker_999", "family_group": "evil_family",
		}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	for _, step := range []struct {
		tool string
		args map[string]interface{}
	}{
		{"create_calendar_event", spoofed(nil)},
		{"update_calendar_event", spoofed(map[string]interface{}{"event_id": 1})},
		{"update_calendar_event", spoofed(map[string]interface{}{"google_event_id": "event-456"})},
		{"delete_calendar_event", spoofed(map[string]interface{}{"google_event_id": "event-456"})},
		{"delete_calendar_event", spoofed(map[string]interface{}{"event_id": 1})},
	} {
		factoryUser = ""
		_, err := mod.Execute(context.Background(), callCtx, step.tool, step.args)
		require.NoError(t, err, step.tool)
		assert.Equal(t, "cal_victim_platform", factoryUser, "%s: the factory must get the canonical caller", step.tool)
		ev := lastMutation(t, got)
		assert.Equal(t, "cal_victim_platform", ev.ActorUserID, step.tool)
		assert.Equal(t, "cal_family_legit", ev.FamilyGroup, step.tool)
		assert.Equal(t, callCtx.Scope.HouseholdID(), ev.HouseholdID, step.tool)
	}
}
