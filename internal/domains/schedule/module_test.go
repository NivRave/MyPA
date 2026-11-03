package schedule

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"
)

// fakeScheduleRepo provides a thread-safe, in-memory implementation of db.ScheduleRepository.
type fakeScheduleRepo struct {
	mu        sync.RWMutex
	events    map[uint]*models.ScheduleEvent
	configs   map[string]*models.TopicCalendarConfig
	nextEvtID uint
	nextCfgID uint
}

func newFakeScheduleRepo() *fakeScheduleRepo {
	return &fakeScheduleRepo{
		events:    make(map[uint]*models.ScheduleEvent),
		configs:   make(map[string]*models.TopicCalendarConfig),
		nextEvtID: 1,
		nextCfgID: 1,
	}
}

func (r *fakeScheduleRepo) CreateScheduleEvent(ev *models.ScheduleEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev.ID = r.nextEvtID
	r.nextEvtID++
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	cp := *ev
	r.events[ev.ID] = &cp
	return nil
}

func (r *fakeScheduleRepo) GetScheduleEvent(id uint) (*models.ScheduleEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ev, ok := r.events[id]
	if !ok {
		return nil, nil
	}
	cp := *ev
	return &cp, nil
}

func (r *fakeScheduleRepo) UpdateScheduleEvent(ev *models.ScheduleEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.events[ev.ID]; !ok {
		return fmt.Errorf("event %d not found", ev.ID)
	}
	cp := *ev
	r.events[ev.ID] = &cp
	return nil
}

func (r *fakeScheduleRepo) DeleteScheduleEvent(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.events, id)
	return nil
}

func (r *fakeScheduleRepo) ListScheduleEvents(userID, familyGroup, topic string, start, end time.Time) ([]models.ScheduleEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var res []models.ScheduleEvent
	normTopic := strings.ToLower(strings.TrimSpace(topic))

	for _, e := range r.events {
		// User & Family filter
		if userID != "" && familyGroup != "" {
			if e.UserID != userID && !(e.FamilyGroup == familyGroup && e.IsShared) {
				continue
			}
		} else if userID != "" && e.UserID != userID {
			continue
		} else if familyGroup != "" && e.FamilyGroup != familyGroup {
			continue
		}

		// Topic filter
		if normTopic != "" && normTopic != "all" && !strings.EqualFold(e.Topic, normTopic) {
			continue
		}

		// Time range filter
		if !start.IsZero() && !end.IsZero() {
			if e.StartTime.After(end) || (!e.EndTime.IsZero() && e.EndTime.Before(start)) {
				continue
			}
		} else if !start.IsZero() && e.StartTime.Before(start) && (e.EndTime.IsZero() || e.EndTime.Before(start)) {
			continue
		} else if !end.IsZero() && e.StartTime.After(end) {
			continue
		}

		res = append(res, *e)
	}
	return res, nil
}

func (r *fakeScheduleRepo) UpsertTopicCalendarConfig(cfg *models.TopicCalendarConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprintf("%s:%s", cfg.UserID, strings.ToLower(strings.TrimSpace(cfg.Topic)))
	if existing, ok := r.configs[key]; ok {
		cfg.ID = existing.ID
	} else {
		cfg.ID = r.nextCfgID
		r.nextCfgID++
	}
	cfg.UpdatedAt = time.Now()
	cp := *cfg
	r.configs[key] = &cp
	return nil
}

func (r *fakeScheduleRepo) GetTopicCalendarConfig(userID, topic string) (*models.TopicCalendarConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", userID, strings.ToLower(strings.TrimSpace(topic)))
	cfg, ok := r.configs[key]
	if !ok {
		return nil, nil
	}
	cp := *cfg
	return &cp, nil
}

func (r *fakeScheduleRepo) ListTopicCalendarConfigs(userID string) ([]models.TopicCalendarConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var res []models.TopicCalendarConfig
	for _, c := range r.configs {
		if userID == "" || c.UserID == userID {
			res = append(res, *c)
		}
	}
	return res, nil
}

// fakeCalendarClient tracks calls and satisfies ColoredCalendarClient.
type fakeCalendarClient struct {
	mu             sync.Mutex
	createdEvents  []models.CalendarEvent
	createdColors  []string
	updatedEvents  map[string]models.CalendarEvent
	deletedEvents  []string
	eventsToList   []*calendar.Event
	nextEventIndex int
}

func newFakeCalendarClient() *fakeCalendarClient {
	return &fakeCalendarClient{
		updatedEvents: make(map[string]models.CalendarEvent),
	}
}

func (c *fakeCalendarClient) CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error) {
	return c.CreateEventWithColor(ctx, ev, "")
}

func (c *fakeCalendarClient) CreateEventWithColor(ctx context.Context, ev models.CalendarEvent, colorID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextEventIndex++
	eventID := fmt.Sprintf("gcal_evt_%d", c.nextEventIndex)
	c.createdEvents = append(c.createdEvents, ev)
	c.createdColors = append(c.createdColors, colorID)
	return eventID, nil
}

func (c *fakeCalendarClient) ListEvents(ctx context.Context, timeMin, timeMax string, q string) ([]*calendar.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eventsToList, nil
}

func (c *fakeCalendarClient) UpdateEvent(ctx context.Context, eventID string, ev models.CalendarEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updatedEvents[eventID] = ev
	return nil
}

func (c *fakeCalendarClient) DeleteEvent(ctx context.Context, eventID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletedEvents = append(c.deletedEvents, eventID)
	return nil
}

// TestScheduleRepository_CRUD validates repository CRUD operations.
func TestScheduleRepository_CRUD(t *testing.T) {
	repo := newFakeScheduleRepo()

	now := time.Now().Truncate(time.Second)
	ev := &models.ScheduleEvent{
		UserID:       "usr_1",
		FamilyGroup:  "Smiths",
		Topic:        "baby",
		Title:        "Pediatrician Visit",
		StartTime:    now,
		EndTime:      now.Add(1 * time.Hour),
		SyncToGoogle: true,
		IsShared:     true,
	}
	require.NoError(t, ev.SetTags([]string{"pediatrician", "checkup"}))

	// Create
	err := repo.CreateScheduleEvent(ev)
	require.NoError(t, err)
	assert.Equal(t, uint(1), ev.ID)

	// Read
	got, err := repo.GetScheduleEvent(1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Pediatrician Visit", got.Title)
	assert.Equal(t, []string{"pediatrician", "checkup"}, got.Tags())

	// Update
	got.Title = "Pediatrician 6-Month Checkup"
	err = repo.UpdateScheduleEvent(got)
	require.NoError(t, err)

	updated, err := repo.GetScheduleEvent(1)
	require.NoError(t, err)
	assert.Equal(t, "Pediatrician 6-Month Checkup", updated.Title)

	// List
	list, err := repo.ListScheduleEvents("usr_1", "Smiths", "baby", now.Add(-1*time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Delete
	err = repo.DeleteScheduleEvent(1)
	require.NoError(t, err)

	deleted, err := repo.GetScheduleEvent(1)
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// Topic Calendar Config CRUD
	syncDefault := true
	cfg := &models.TopicCalendarConfig{
		UserID:                   "usr_1",
		Topic:                    "baby",
		PrefixTag:                "Baby",
		ColorID:                  "4",
		TargetCalendarID:         "primary",
		IncludeInMorningBriefing: true,
		SyncToGoogleDefault:      &syncDefault,
	}
	err = repo.UpsertTopicCalendarConfig(cfg)
	require.NoError(t, err)

	cfgGot, err := repo.GetTopicCalendarConfig("usr_1", "baby")
	require.NoError(t, err)
	require.NotNil(t, cfgGot)
	assert.Equal(t, "Baby", cfgGot.PrefixTag)
	assert.Equal(t, "4", cfgGot.ColorID)

	configs, err := repo.ListTopicCalendarConfigs("usr_1")
	require.NoError(t, err)
	assert.Len(t, configs, 1)
}

// TestModule_VirtualSubCalendar_PrefixingAndColor tests [Topic] title prefixing and color ID.
func TestModule_VirtualSubCalendar_PrefixingAndColor(t *testing.T) {
	repo := newFakeScheduleRepo()
	calMock := newFakeCalendarClient()

	user := &models.User{
		ID:          "uuid_1",
		PlatformID:  "tg_123",
		Name:        "Alice",
		FamilyGroup: "AliceFamily",
	}

	// Configure topic calendar for 'baby'
	err := repo.UpsertTopicCalendarConfig(&models.TopicCalendarConfig{
		UserID:    "tg_123",
		Topic:     "baby",
		PrefixTag: "Baby",
		ColorID:   "4",
	})
	require.NoError(t, err)

	mod := NewModule(repo, WithCalendarFactory(func(ctx context.Context, uid string) (orchestrator.CalendarClient, error) {
		return calMock, nil
	}))

	callCtx := orchestrator.CallContext{
		User:    user,
		Message: models.Message{UserID: "tg_123"},
	}

	// 1. Title without prefix should receive [Baby]
	res, err := mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title":          "Pediatrician Checkup",
		"topic":          "baby",
		"start_time":     "2026-11-04T10:00:00Z",
		"end_time":       "2026-11-04T11:00:00Z",
		"sync_to_google": true,
		"is_shared":      false,
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Scheduled Event Created")
	assert.Contains(t, res, "[Baby] Pediatrician Checkup")

	require.Len(t, calMock.createdEvents, 1)
	assert.Equal(t, "[Baby] Pediatrician Checkup", calMock.createdEvents[0].Title)
	assert.Equal(t, "4", calMock.createdColors[0])

	// 2. Title already containing a bracket prefix should NOT be double prefixed
	_, err = mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title":          "[Baby] Vaccine Shot",
		"topic":          "baby",
		"start_time":     "2026-11-05T10:00:00Z",
		"sync_to_google": true,
		"is_shared":      false,
	})
	require.NoError(t, err)
	require.Len(t, calMock.createdEvents, 2)
	assert.Equal(t, "[Baby] Vaccine Shot", calMock.createdEvents[1].Title)
}

// TestModule_InternalOnly_Schedules tests that high-frequency routines (sync_to_google: false)
// do not touch Google Calendar and store only in DB.
func TestModule_InternalOnly_Schedules(t *testing.T) {
	repo := newFakeScheduleRepo()
	calMock := newFakeCalendarClient()

	user := &models.User{
		PlatformID:  "tg_123",
		FamilyGroup: "AliceFamily",
	}

	mod := NewModule(repo, WithCalendarFactory(func(ctx context.Context, uid string) (orchestrator.CalendarClient, error) {
		return calMock, nil
	}))

	callCtx := orchestrator.CallContext{
		User: user,
	}

	res, err := mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title":          "Afternoon Feed 150ml",
		"topic":          "baby",
		"start_time":     "2026-11-04T14:30:00Z",
		"tags":           []interface{}{"feeding", "formula"},
		"sync_to_google": false,
		"is_shared":      false,
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Internal-only (database)")

	// Google Calendar should not have been called
	assert.Empty(t, calMock.createdEvents)

	// Database should contain the event
	events, err := repo.ListScheduleEvents("tg_123", "AliceFamily", "baby", time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Afternoon Feed 150ml", events[0].Title)
	assert.False(t, events[0].SyncToGoogle)
	assert.Empty(t, events[0].ExternalGoogleEventID)
	assert.Equal(t, []string{"feeding", "formula"}, events[0].Tags())
}

// TestModule_PartnerCalendarSync tests automatic synchronization to partner's Google Calendar.
func TestModule_PartnerCalendarSync(t *testing.T) {
	repo := newFakeScheduleRepo()
	userCalMock := newFakeCalendarClient()
	partnerCalMock := newFakeCalendarClient()

	user := &models.User{
		ID:          "usr_1",
		PlatformID:  "tg_alice",
		Name:        "Alice",
		FamilyGroup: "Smiths",
	}

	partner := &models.User{
		ID:          "usr_2",
		PlatformID:  "tg_bob",
		Name:        "Bob",
		FamilyGroup: "Smiths",
	}

	mod := NewModule(repo, WithCalendarFactory(func(ctx context.Context, uid string) (orchestrator.CalendarClient, error) {
		if uid == "tg_alice" {
			return userCalMock, nil
		}
		if uid == "tg_bob" {
			return partnerCalMock, nil
		}
		return nil, fmt.Errorf("user not found")
	}))

	callCtx := orchestrator.CallContext{
		User: user,
		Extra: map[string]interface{}{
			"partner_user_id": partner.PlatformID,
		},
	}

	res, err := mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title":          "Boiler Inspection",
		"topic":          "house",
		"start_time":     "2026-11-06T09:00:00Z",
		"sync_to_google": true,
		"is_shared":      true,
	})
	require.NoError(t, err)
	assert.Contains(t, res, "**Partner Calendar**: Synced ✅")

	// Both Alice's and Bob's calendars should receive the event
	require.Len(t, userCalMock.createdEvents, 1)
	assert.Equal(t, "[House] Boiler Inspection", userCalMock.createdEvents[0].Title)

	require.Len(t, partnerCalMock.createdEvents, 1)
	assert.Equal(t, "[House] Boiler Inspection", partnerCalMock.createdEvents[0].Title)

	// Database record should have both IDs stored
	events, err := repo.ListScheduleEvents("tg_alice", "Smiths", "house", time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].ExternalGoogleEventID)
	assert.NotEmpty(t, events[0].PartnerGoogleEventID)
}

// TestModule_EventBus_Mutations verifies that MutationEvents with Topic "schedule" are published.
func TestModule_EventBus_Mutations(t *testing.T) {
	repo := newFakeScheduleRepo()
	bus := events.NewEventBus()

	var published []events.MutationEvent
	var mu sync.Mutex
	bus.SubscribeTopic("schedule", func(ctx context.Context, ev events.MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, ev)
		return nil
	})

	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User:     &models.User{PlatformID: "usr_alice", FamilyGroup: "Family1"},
		EventBus: bus,
	}

	// 1. Create event
	_, err := mod.Execute(context.Background(), callCtx, "schedule_topic_event", map[string]interface{}{
		"title":          "Gym Session",
		"topic":          "health",
		"start_time":     "2026-11-04T07:00:00Z",
		"sync_to_google": false,
	})
	require.NoError(t, err)

	// 2. Configure topic
	_, err = mod.Execute(context.Background(), callCtx, "configure_topic_calendar", map[string]interface{}{
		"topic":      "health",
		"prefix_tag": "Fitness",
		"color_id":   "2",
	})
	require.NoError(t, err)

	// 3. Update event
	_, err = mod.Execute(context.Background(), callCtx, "update_topic_event", map[string]interface{}{
		"event_id": 1,
		"title":    "Intense Gym Session",
	})
	require.NoError(t, err)

	// 4. Delete event
	_, err = mod.Execute(context.Background(), callCtx, "delete_topic_event", map[string]interface{}{
		"event_id": 1,
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, published, 4)
	assert.Equal(t, "create", published[0].Action)
	assert.Equal(t, "ScheduleEvent", published[0].EntityType)
	assert.Equal(t, "schedule", published[0].Topic)

	assert.Equal(t, "configure", published[1].Action)
	assert.Equal(t, "TopicCalendarConfig", published[1].EntityType)

	assert.Equal(t, "update", published[2].Action)
	assert.Equal(t, "ScheduleEvent", published[2].EntityType)

	assert.Equal(t, "delete", published[3].Action)
	assert.Equal(t, "ScheduleEvent", published[3].EntityType)
}

// TestModule_ListTopicSchedule_Merging tests merging DB events with Google Calendar events.
func TestModule_ListTopicSchedule_Merging(t *testing.T) {
	repo := newFakeScheduleRepo()
	calMock := newFakeCalendarClient()

	now := time.Now().Truncate(time.Hour)

	// DB internal event
	err := repo.CreateScheduleEvent(&models.ScheduleEvent{
		UserID:       "usr_1",
		Topic:        "baby",
		Title:        "Morning Feeding",
		StartTime:    now.Add(1 * time.Hour),
		EndTime:      now.Add(2 * time.Hour),
		SyncToGoogle: false,
	})
	require.NoError(t, err)

	// Google Calendar external event
	calMock.eventsToList = []*calendar.Event{
		{
			Id:          "gcal_ext_1",
			Summary:     "[Baby] Pediatrician Follow-up",
			Description: "Clinic visit",
			Start:       &calendar.EventDateTime{DateTime: now.Add(3 * time.Hour).Format(time.RFC3339)},
			End:         &calendar.EventDateTime{DateTime: now.Add(4 * time.Hour).Format(time.RFC3339)},
		},
		{
			Id:          "gcal_ext_2",
			Summary:     "[Work] Team Standup",
			Description: "Engineering sync",
			Start:       &calendar.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
			End:         &calendar.EventDateTime{DateTime: now.Add(3 * time.Hour).Format(time.RFC3339)},
		},
	}

	mod := NewModule(repo, WithCalendarFactory(func(ctx context.Context, uid string) (orchestrator.CalendarClient, error) {
		return calMock, nil
	}))

	callCtx := orchestrator.CallContext{
		User: &models.User{PlatformID: "usr_1"},
	}

	// Query for topic 'baby'
	res, err := mod.Execute(context.Background(), callCtx, "list_topic_schedule", map[string]interface{}{
		"topic":          "baby",
		"include_google": true,
	})
	require.NoError(t, err)

	assert.Contains(t, res, "Morning Feeding")
	assert.Contains(t, res, "Pediatrician Follow-up")
	// Work standup should not be in the 'baby' output
	assert.NotContains(t, res, "Team Standup")
}

// TestModule_SystemPromptSection tests dynamic system prompt generation.
func TestModule_SystemPromptSection(t *testing.T) {
	repo := newFakeScheduleRepo()

	user := &models.User{
		PlatformID:  "usr_1",
		FamilyGroup: "MyFamily",
	}

	now := time.Now()
	// Add config
	_ = repo.UpsertTopicCalendarConfig(&models.TopicCalendarConfig{
		UserID:    "usr_1",
		Topic:     "baby",
		PrefixTag: "Baby",
		ColorID:   "4",
	})

	// Add upcoming event
	_ = repo.CreateScheduleEvent(&models.ScheduleEvent{
		UserID:       "usr_1",
		Topic:        "baby",
		Title:        "Pediatrician Checkup",
		StartTime:    now.Add(4 * time.Hour),
		EndTime:      now.Add(5 * time.Hour),
		SyncToGoogle: true,
	})

	mod := NewModule(repo)
	callCtx := orchestrator.CallContext{User: user}

	prompt := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, prompt, "Multi-Schedule Engine & Virtual Sub-Calendars")
	assert.Contains(t, prompt, "Prefix `[Baby]`, Color ID `4`")
	assert.Contains(t, prompt, "Pediatrician Checkup")
}
