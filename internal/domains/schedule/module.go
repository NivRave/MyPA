package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// CalendarFactory is a function that produces a CalendarClient for a given user ID.
type CalendarFactory func(ctx context.Context, userID string) (orchestrator.CalendarClient, error)

// ColoredCalendarClient is an optional extension interface for CalendarClients that
// support specifying Google Calendar color IDs.
type ColoredCalendarClient interface {
	orchestrator.CalendarClient
	CreateEventWithColor(ctx context.Context, ev models.CalendarEvent, colorID string) (string, error)
}

// Option configures a Module instance.
type Option func(*Module)

// WithCalendarFactory sets a custom CalendarFactory on the module.
func WithCalendarFactory(f CalendarFactory) Option {
	return func(m *Module) {
		m.calendarFactory = f
	}
}

// WithEventBus sets the event bus on the module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for multi-schedule and virtual sub-calendar management.
type Module struct {
	repo            db.ScheduleRepository
	calendarFactory CalendarFactory
	bus             *events.EventBus
	mu              sync.RWMutex
}

// NewModule initializes a new Module with the given repository and options.
func NewModule(repo db.ScheduleRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique module identifier.
func (m *Module) Name() string {
	return "schedule"
}

// Declarations returns the Gemini FunctionDeclaration schemas for schedule tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name: "schedule_topic_event",
			Description: "Create a topic-tagged schedule event with virtual sub-calendar support. Events can be synchronized with Google Calendar (with topic prefix e.g. [Baby], [House] and custom color coding) or kept internal-only for high-frequency routines (baby feedings, meds, workouts). If shared, also synchronizes to partner's calendar.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "Title or summary of the event (e.g. 'Pediatrician Checkup', 'Plumber Visit', 'Morning Feeding').",
					},
					"topic": {
						Type:        genai.TypeString,
						Description: "Life domain or topic (e.g. 'baby', 'house', 'finance', 'health', 'work', 'personal', 'family').",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start time in ISO 8601 / RFC3339 format (e.g. '2026-11-04T10:00:00Z').",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Optional end time in ISO 8601 / RFC3339 format. If omitted, defaults to 1 hour after start time.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Optional notes or details about the event.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional sub-tags for filtering (e.g. ['pediatrician', 'vaccine'], ['maintenance', 'plumber']).",
					},
					"sync_to_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to sync to Google Calendar. Set to false for internal-only schedules that shouldn't clutter the primary calendar.",
					},
					"is_shared": {
						Type:        genai.TypeBoolean,
						Description: "Whether this event is shared with family/partner. When true, syncs to partner's Google calendar as well.",
					},
					"recurrence": {
						Type:        genai.TypeString,
						Description: "Optional recurrence rule in RRULE format (e.g. 'RRULE:FREQ=DAILY;INTERVAL=1').",
					},
				},
				Required: []string{"title", "topic", "start_time"},
			},
		},
		{
			Name: "list_topic_schedule",
			Description: "Query and list schedule events filtered by topic, tags, and time range. Merges database schedule events with Google Calendar events.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Topic to filter by (e.g. 'baby', 'house', 'finance', 'work', 'all').",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional tags to filter by.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Start time range in ISO 8601 / RFC3339 format. Defaults to start of today.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "End time range in ISO 8601 / RFC3339 format. Defaults to 30 days ahead.",
					},
					"include_google": {
						Type:        genai.TypeBoolean,
						Description: "Whether to merge events directly from Google Calendar (default true).",
					},
				},
			},
		},
		{
			Name: "configure_topic_calendar",
			Description: "Customize virtual sub-calendar settings for a topic: custom bracket prefix tag, Google Calendar color ID (1-11), internal-only vs Google sync default, and morning briefing inclusion.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Topic to configure (e.g. 'baby', 'house', 'finance', 'health', 'work').",
					},
					"prefix_tag": {
						Type:        genai.TypeString,
						Description: "Custom prefix bracket tag for calendar event titles (e.g. 'Baby' results in '[Baby] Event Title').",
					},
					"color_id": {
						Type:        genai.TypeString,
						Description: "Google Calendar event color ID (e.g. '1' for Lavender, '2' for Sage, '4' for Flamingo, etc.).",
					},
					"target_calendar_id": {
						Type:        genai.TypeString,
						Description: "Optional Google Calendar ID if routing events to a dedicated sub-calendar instead of 'primary'.",
					},
					"sync_to_google_default": {
						Type:        genai.TypeBoolean,
						Description: "Default sync_to_google setting for this topic (true = sync to Google, false = internal database only).",
					},
					"include_in_morning_briefing": {
						Type:        genai.TypeBoolean,
						Description: "Whether events in this topic should be summarized in the daily morning briefing.",
					},
				},
				Required: []string{"topic"},
			},
		},
		{
			Name: "delete_topic_event",
			Description: "Delete a topic-tagged schedule event by ID, removing it from the database and any synced Google Calendars (including partner's calendar).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"event_id": {
						Type:        genai.TypeInteger,
						Description: "The numeric ID of the schedule event to delete.",
					},
				},
				Required: []string{"event_id"},
			},
		},
		{
			Name: "update_topic_event",
			Description: "Update an existing schedule event's title, time, notes, tags, or sync status across database and Google Calendars.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"event_id": {
						Type:        genai.TypeInteger,
						Description: "The numeric ID of the schedule event to update.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "Updated event title.",
					},
					"start_time": {
						Type:        genai.TypeString,
						Description: "Updated start time in ISO 8601 format.",
					},
					"end_time": {
						Type:        genai.TypeString,
						Description: "Updated end time in ISO 8601 format.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Updated description or notes.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Updated tags.",
					},
				},
				Required: []string{"event_id"},
			},
		},
	}
}

// resolveRepo determines the active db.ScheduleRepository from module fields or call context.
func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.ScheduleRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.ScheduleRepository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["schedule_repo"].(db.ScheduleRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("schedule: no schedule repository available in module or call context")
}

// resolveCalendarClient retrieves a CalendarClient for the target user ID.
func (m *Module) resolveCalendarClient(ctx context.Context, callCtx orchestrator.CallContext, userID string) (orchestrator.CalendarClient, error) {
	if m.calendarFactory != nil {
		return m.calendarFactory(ctx, userID)
	}
	if factory, ok := callCtx.Extra["calendar_factory"].(func(context.Context, string) (orchestrator.CalendarClient, error)); ok {
		return factory(ctx, userID)
	}
	if factory, ok := callCtx.Extra["calendar_factory"].(CalendarFactory); ok {
		return factory(ctx, userID)
	}

	activeUserID := ""
	if callCtx.User != nil {
		activeUserID = callCtx.User.PlatformID
		if activeUserID == "" {
			activeUserID = callCtx.User.ID
		}
	}
	if activeUserID == "" {
		activeUserID = callCtx.Message.UserID
	}

	if userID == activeUserID {
		if client, ok := callCtx.Extra["calendar_client"].(orchestrator.CalendarClient); ok {
			return client, nil
		}
	} else {
		if client, ok := callCtx.Extra["partner_calendar_client"].(orchestrator.CalendarClient); ok {
			return client, nil
		}
	}
	return nil, nil
}

// resolveEventBus retrieves the active EventBus.
func (m *Module) resolveEventBus(callCtx orchestrator.CallContext) *events.EventBus {
	if m.bus != nil {
		return m.bus
	}
	if callCtx.EventBus != nil {
		return callCtx.EventBus
	}
	if bus, ok := callCtx.Extra["event_bus"].(*events.EventBus); ok {
		return bus
	}
	return nil
}

// Execute dispatches and runs tools provided by the schedule module.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "schedule_topic_event":
		return m.executeScheduleTopicEvent(ctx, callCtx, repo, args)
	case "list_topic_schedule":
		return m.executeListTopicSchedule(ctx, callCtx, repo, args)
	case "configure_topic_calendar":
		return m.executeConfigureTopicCalendar(ctx, callCtx, repo, args)
	case "delete_topic_event":
		return m.executeDeleteTopicEvent(ctx, callCtx, repo, args)
	case "update_topic_event":
		return m.executeUpdateTopicEvent(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("schedule: unknown tool %q", toolName)
	}
}

func (m *Module) executeScheduleTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	title := strings.TrimSpace(getStringArg(args, "title"))
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	if topic == "" {
		topic = "general"
	}

	startTimeStr := strings.TrimSpace(getStringArg(args, "start_time"))
	if startTimeStr == "" {
		return "", fmt.Errorf("start_time is required")
	}
	startTime, err := parseFlexibleTime(startTimeStr)
	if err != nil {
		return "", fmt.Errorf("invalid start_time %q: %w", startTimeStr, err)
	}

	endTimeStr := strings.TrimSpace(getStringArg(args, "end_time"))
	var endTime time.Time
	if endTimeStr != "" {
		endTime, err = parseFlexibleTime(endTimeStr)
		if err != nil {
			return "", fmt.Errorf("invalid end_time %q: %w", endTimeStr, err)
		}
	} else {
		endTime = startTime.Add(1 * time.Hour)
	}

	description := strings.TrimSpace(getStringArg(args, "description"))
	tags := getStringSliceArg(args, "tags")
	recurrence := strings.TrimSpace(getStringArg(args, "recurrence"))

	// Resolve user identity
	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.UserID
	}

	// Topic configuration lookup
	topicCfg, _ := repo.GetTopicCalendarConfig(userID, topic)

	// Sync to Google default determination
	syncToGoogle := true
	if val, ok := args["sync_to_google"].(bool); ok {
		syncToGoogle = val
	} else if topicCfg != nil && topicCfg.SyncToGoogleDefault != nil {
		syncToGoogle = *topicCfg.SyncToGoogleDefault
	}

	// Shared topic determination
	isShared := false
	if val, ok := args["is_shared"].(bool); ok {
		isShared = val
	} else if familyGroup != "" && models.IsDefaultSharedTopic(topic) {
		isShared = true
	}

	prefixTag := ""
	colorID := ""
	if topicCfg != nil {
		prefixTag = topicCfg.PrefixTag
		colorID = topicCfg.ColorID
	}

	ev := &models.ScheduleEvent{
		UserID:          userID,
		FamilyGroup:     familyGroup,
		Topic:           topic,
		Title:           title,
		Description:     description,
		StartTime:       startTime,
		EndTime:         endTime,
		RecurrenceRule:  recurrence,
		SyncToGoogle:    syncToGoogle,
		IsShared:        isShared,
		CreatedByUserID: userID,
		CreatedAt:       time.Now(),
	}
	_ = ev.SetTags(tags)

	formattedTitle := ev.FormattedTitle(prefixTag)

	// Virtual Sub-Calendar: Google Calendar sync for primary user
	if syncToGoogle {
		calClient, _ := m.resolveCalendarClient(ctx, callCtx, userID)
		if calClient != nil {
			calEv := models.CalendarEvent{
				Title:       formattedTitle,
				Description: description,
				StartTime:   startTime.Format(time.RFC3339),
				EndTime:     endTime.Format(time.RFC3339),
				Timezone:    "UTC",
			}
			if recurrence != "" {
				calEv.Recurrence = []string{recurrence}
			}

			var googleID string
			if coloredCal, ok := calClient.(ColoredCalendarClient); ok && colorID != "" {
				googleID, err = coloredCal.CreateEventWithColor(ctx, calEv, colorID)
			} else {
				googleID, err = calClient.CreateEvent(ctx, calEv)
			}
			if err == nil && googleID != "" {
				ev.ExternalGoogleEventID = googleID
			}
		}
	}

	// Partner Calendar Sync (Topic 9.3)
	if isShared && syncToGoogle && familyGroup != "" {
		partnerUsers := m.findPartnerUsers(callCtx, userID, familyGroup)
		for _, partner := range partnerUsers {
			partnerCal, _ := m.resolveCalendarClient(ctx, callCtx, partner.PlatformID)
			if partnerCal != nil {
				calEv := models.CalendarEvent{
					Title:       formattedTitle,
					Description: description,
					StartTime:   startTime.Format(time.RFC3339),
					EndTime:     endTime.Format(time.RFC3339),
					Timezone:    "UTC",
				}
				if recurrence != "" {
					calEv.Recurrence = []string{recurrence}
				}

				var partnerID string
				if coloredCal, ok := partnerCal.(ColoredCalendarClient); ok && colorID != "" {
					partnerID, err = coloredCal.CreateEventWithColor(ctx, calEv, colorID)
				} else {
					partnerID, err = partnerCal.CreateEvent(ctx, calEv)
				}
				if err == nil && partnerID != "" {
					ev.PartnerGoogleEventID = partnerID
					break
				}
			}
		}
	}

	// Save to DB
	if err := repo.CreateScheduleEvent(ev); err != nil {
		return "", fmt.Errorf("failed to save schedule event: %w", err)
	}

	// Emit MutationEvent
	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(ev)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "create",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(ev.ID), 10),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Scheduled '%s' under topic '%s' for %s", formattedTitle, ev.Topic, ev.StartTime.Format(time.RFC822)),
			Timestamp:   time.Now(),
		})
	}

	// Build response message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 **Scheduled Event Created** (ID: %d)\n", ev.ID))
	sb.WriteString(fmt.Sprintf("• **Title**: %s\n", formattedTitle))
	sb.WriteString(fmt.Sprintf("• **Topic**: %s\n", ev.Topic))
	sb.WriteString(fmt.Sprintf("• **Start**: %s\n", ev.StartTime.Format(time.RFC1123)))
	sb.WriteString(fmt.Sprintf("• **End**: %s\n", ev.EndTime.Format(time.RFC1123)))

	if len(tags) > 0 {
		sb.WriteString(fmt.Sprintf("• **Tags**: %s\n", strings.Join(tags, ", ")))
	}
	if description != "" {
		sb.WriteString(fmt.Sprintf("• **Details**: %s\n", description))
	}

	if ev.SyncToGoogle {
		if ev.ExternalGoogleEventID != "" {
			sb.WriteString("• **Google Calendar**: Synced ✅\n")
		} else {
			sb.WriteString("• **Google Calendar**: Sync enabled (offline/pending)\n")
		}
	} else {
		sb.WriteString("• **Google Calendar**: Internal-only (database)\n")
	}

	if ev.IsShared {
		if ev.PartnerGoogleEventID != "" {
			sb.WriteString("• **Partner Calendar**: Synced ✅\n")
		} else {
			sb.WriteString("• **Partner Calendar**: Shared in family space\n")
		}
	}

	return sb.String(), nil
}

func (m *Module) executeListTopicSchedule(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	tagFilter := getStringSliceArg(args, "tags")
	includeGoogle := getBoolArg(args, "include_google", true)

	startTimeStr := strings.TrimSpace(getStringArg(args, "start_time"))
	var startTime time.Time
	if startTimeStr != "" {
		startTime, _ = parseFlexibleTime(startTimeStr)
	}
	if startTime.IsZero() {
		startTime = time.Now().Truncate(24 * time.Hour)
	}

	endTimeStr := strings.TrimSpace(getStringArg(args, "end_time"))
	var endTime time.Time
	if endTimeStr != "" {
		endTime, _ = parseFlexibleTime(endTimeStr)
	}
	if endTime.IsZero() {
		endTime = startTime.Add(30 * 24 * time.Hour)
	}

	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.UserID
	}

	eventsList, err := repo.ListScheduleEvents(userID, familyGroup, topic, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query schedule events: %w", err)
	}

	// Filter by tags if specified
	if len(tagFilter) > 0 {
		var filtered []models.ScheduleEvent
		for _, e := range eventsList {
			eventTags := e.Tags()
			if hasAnyTag(eventTags, tagFilter) {
				filtered = append(filtered, e)
			}
		}
		eventsList = filtered
	}

	// Merge with Google Calendar events if enabled
	if includeGoogle {
		calClient, _ := m.resolveCalendarClient(ctx, callCtx, userID)
		if calClient != nil {
			gEvents, err := calClient.ListEvents(ctx, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), "")
			if err == nil {
				knownIDs := make(map[string]bool)
				for _, e := range eventsList {
					if e.ExternalGoogleEventID != "" {
						knownIDs[e.ExternalGoogleEventID] = true
					}
					if e.PartnerGoogleEventID != "" {
						knownIDs[e.PartnerGoogleEventID] = true
					}
				}

				for _, ge := range gEvents {
					if ge == nil || knownIDs[ge.Id] || knownIDs[ge.HtmlLink] {
						continue
					}

					// Extract topic from [Topic] prefix if present
					summary := strings.TrimSpace(ge.Summary)
					evTopic := "general"
					if strings.HasPrefix(summary, "[") && strings.Contains(summary, "]") {
						endIdx := strings.Index(summary, "]")
						evTopic = strings.ToLower(strings.TrimSpace(summary[1:endIdx]))
					}

					if topic != "" && !strings.EqualFold(topic, "all") && !strings.EqualFold(topic, evTopic) {
						continue
					}

					var gStart, gEnd time.Time
					if ge.Start != nil {
						if ge.Start.DateTime != "" {
							gStart, _ = time.Parse(time.RFC3339, ge.Start.DateTime)
						} else if ge.Start.Date != "" {
							gStart, _ = time.Parse("2006-01-02", ge.Start.Date)
						}
					}
					if ge.End != nil {
						if ge.End.DateTime != "" {
							gEnd, _ = time.Parse(time.RFC3339, ge.End.DateTime)
						} else if ge.End.Date != "" {
							gEnd, _ = time.Parse("2006-01-02", ge.End.Date)
						}
					}

					eventsList = append(eventsList, models.ScheduleEvent{
						ExternalGoogleEventID: ge.Id,
						UserID:                userID,
						Topic:                 evTopic,
						Title:                 summary,
						Description:           ge.Description,
						StartTime:             gStart,
						EndTime:               gEnd,
						SyncToGoogle:          true,
					})
				}
			}
		}
	}

	// Sort merged list by StartTime
	sort.Slice(eventsList, func(i, j int) bool {
		return eventsList[i].StartTime.Before(eventsList[j].StartTime)
	})

	if len(eventsList) == 0 {
		topicLabel := topic
		if topicLabel == "" {
			topicLabel = "all"
		}
		return fmt.Sprintf("No scheduled events found for topic '%s' between %s and %s.", topicLabel, startTime.Format("Jan 02"), endTime.Format("Jan 02")), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 **Schedule Events (%d found)**:\n\n", len(eventsList)))
	for i, e := range eventsList {
		status := "Internal (DB)"
		if e.SyncToGoogle {
			status = "Google Synced"
		}
		if e.PartnerGoogleEventID != "" {
			status += " + Partner Synced"
		}

		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, e.Title))
		sb.WriteString(fmt.Sprintf("   • **Topic**: `%s` | **Status**: %s\n", e.Topic, status))
		sb.WriteString(fmt.Sprintf("   • **When**: %s – %s\n", e.StartTime.Format("Mon, Jan 02 15:04"), e.EndTime.Format("15:04 MST")))
		if tags := e.Tags(); len(tags) > 0 {
			sb.WriteString(fmt.Sprintf("   • **Tags**: %s\n", strings.Join(tags, ", ")))
		}
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   • **Notes**: %s\n", e.Description))
		}
		if e.ID > 0 {
			sb.WriteString(fmt.Sprintf("   • **ID**: `%d`\n", e.ID))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

func (m *Module) executeConfigureTopicCalendar(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	userID := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
	}
	if userID == "" {
		userID = callCtx.Message.UserID
	}

	cfg, _ := repo.GetTopicCalendarConfig(userID, topic)
	if cfg == nil {
		cfg = &models.TopicCalendarConfig{
			UserID:                   userID,
			Topic:                    topic,
			PrefixTag:                strings.ToUpper(topic[:1]) + strings.ToLower(topic[1:]),
			TargetCalendarID:         "primary",
			IncludeInMorningBriefing: true,
			UpdatedAt:                time.Now(),
		}
	}

	if prefix := strings.TrimSpace(getStringArg(args, "prefix_tag")); prefix != "" {
		cfg.PrefixTag = prefix
	}
	if color := strings.TrimSpace(getStringArg(args, "color_id")); color != "" {
		cfg.ColorID = color
	}
	if targetCal := strings.TrimSpace(getStringArg(args, "target_calendar_id")); targetCal != "" {
		cfg.TargetCalendarID = targetCal
	}
	if val, ok := args["sync_to_google_default"].(bool); ok {
		cfg.SyncToGoogleDefault = &val
	}
	if val, ok := args["include_in_morning_briefing"].(bool); ok {
		cfg.IncludeInMorningBriefing = val
	}

	if err := repo.UpsertTopicCalendarConfig(cfg); err != nil {
		return "", fmt.Errorf("failed to save topic calendar config: %w", err)
	}

	// Emit MutationEvent
	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(cfg)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "configure",
			EntityType:  "TopicCalendarConfig",
			EntityID:    strconv.FormatUint(uint64(cfg.ID), 10),
			ActorUserID: userID,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Configured virtual sub-calendar for topic '%s' (Prefix: [%s], Color: %s)", cfg.Topic, cfg.PrefixTag, cfg.ColorID),
			Timestamp:   time.Now(),
		})
	}

	syncDefaultStr := "Google Calendar Synced"
	if cfg.SyncToGoogleDefault != nil && !*cfg.SyncToGoogleDefault {
		syncDefaultStr = "Internal Database Only"
	}

	return fmt.Sprintf("✅ **Topic Calendar Configured for '%s'**:\n• **Prefix Tag**: `[%s]`\n• **Color ID**: `%s`\n• **Target Calendar**: `%s`\n• **Default Sync**: %s\n• **Morning Briefing**: %t",
		cfg.Topic, cfg.PrefixTag, cfg.ColorID, cfg.TargetCalendarID, syncDefaultStr, cfg.IncludeInMorningBriefing), nil
}

func (m *Module) executeDeleteTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventIDRaw, ok := getIntArg(args, "event_id")
	if !ok || eventIDRaw <= 0 {
		return "", fmt.Errorf("valid event_id is required")
	}
	eventID := uint(eventIDRaw)

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	existing, err := repo.GetScheduleEvent(eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UserID != "" && existing.UserID == userID))
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	// Delete from Google Calendar if synced
	if existing.ExternalGoogleEventID != "" {
		calClient, _ := m.resolveCalendarClient(ctx, callCtx, existing.UserID)
		if calClient != nil {
			_ = calClient.DeleteEvent(ctx, existing.ExternalGoogleEventID)
		}
	}

	// Delete from Partner Google Calendar if synced
	if existing.PartnerGoogleEventID != "" && existing.FamilyGroup != "" {
		partnerUsers := m.findPartnerUsers(callCtx, existing.UserID, existing.FamilyGroup)
		for _, partner := range partnerUsers {
			partnerCal, _ := m.resolveCalendarClient(ctx, callCtx, partner.PlatformID)
			if partnerCal != nil {
				_ = partnerCal.DeleteEvent(ctx, existing.PartnerGoogleEventID)
				break
			}
		}
	}

	if err := repo.DeleteScheduleEvent(eventID); err != nil {
		return "", fmt.Errorf("failed to delete schedule event %d: %w", eventID, err)
	}

	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		beforeBytes, _ := json.Marshal(existing)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "delete",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(eventID), 10),
			ActorUserID: existing.UserID,
			FamilyGroup: existing.FamilyGroup,
			BeforeJSON:  string(beforeBytes),
			Summary:     fmt.Sprintf("Deleted schedule event '%s' (topic: %s)", existing.Title, existing.Topic),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("🗑️ Schedule event '%s' (ID: %d) successfully deleted.", existing.Title, eventID), nil
}

func (m *Module) executeUpdateTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventIDRaw, ok := getIntArg(args, "event_id")
	if !ok || eventIDRaw <= 0 {
		return "", fmt.Errorf("valid event_id is required")
	}
	eventID := uint(eventIDRaw)

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	existing, err := repo.GetScheduleEvent(eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UserID != "" && existing.UserID == userID))
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	beforeBytes, _ := json.Marshal(existing)

	if title := strings.TrimSpace(getStringArg(args, "title")); title != "" {
		existing.Title = title
	}
	if startStr := strings.TrimSpace(getStringArg(args, "start_time")); startStr != "" {
		if st, err := parseFlexibleTime(startStr); err == nil {
			existing.StartTime = st
		}
	}
	if endStr := strings.TrimSpace(getStringArg(args, "end_time")); endStr != "" {
		if et, err := parseFlexibleTime(endStr); err == nil {
			existing.EndTime = et
		}
	}
	if desc := strings.TrimSpace(getStringArg(args, "description")); desc != "" {
		existing.Description = desc
	}
	if tags := getStringSliceArg(args, "tags"); len(tags) > 0 {
		_ = existing.SetTags(tags)
	}

	// Update on Google Calendar if event was synced
	if existing.ExternalGoogleEventID != "" {
		calClient, _ := m.resolveCalendarClient(ctx, callCtx, existing.UserID)
		if calClient != nil {
			_ = calClient.UpdateEvent(ctx, existing.ExternalGoogleEventID, models.CalendarEvent{
				Title:       existing.FormattedTitle(""),
				Description: existing.Description,
				StartTime:   existing.StartTime.Format(time.RFC3339),
				EndTime:     existing.EndTime.Format(time.RFC3339),
				Timezone:    "UTC",
			})
		}
	}

	if err := repo.UpdateScheduleEvent(existing); err != nil {
		return "", fmt.Errorf("failed to update event: %w", err)
	}

	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(existing)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "update",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(eventID), 10),
			ActorUserID: existing.UserID,
			FamilyGroup: existing.FamilyGroup,
			BeforeJSON:  string(beforeBytes),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated schedule event '%s' (topic: %s)", existing.Title, existing.Topic),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("✅ Schedule event '%s' (ID: %d) updated.", existing.Title, eventID), nil
}

// SystemPromptSection injects topic calendar conventions, configured topics,
// and upcoming scheduled events into the system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	var sb strings.Builder
	sb.WriteString("### Multi-Schedule Engine & Virtual Sub-Calendars:\n")
	sb.WriteString("• You manage distinct topic-based schedules (`baby`, `house`, `finance`, `health`, `work`, `personal`) using `schedule_topic_event`, `list_topic_schedule`, and `configure_topic_calendar`.\n")
	sb.WriteString("• Virtual sub-calendars: Prefix event titles with `[Topic]` (e.g. `[Baby] Pediatrician Checkup`, `[House] Boiler Inspection`) and assign distinct Google Calendar colors.\n")
	sb.WriteString("• Internal-only schedules: For high-frequency routine logs (baby feedings, diapers, daily medication, workouts), set `sync_to_google: false` to store purely in the database without cluttering the primary calendar.\n")
	sb.WriteString("• Partner Calendar Sync: Topics `baby`, `house`, `finance`, and `family` default to shared (`is_shared: true`). Synced shared events automatically duplicate onto the partner's Google Calendar with attribution.\n")

	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.UserID
	}

	repo, err := m.resolveRepo(callCtx)
	if err == nil && repo != nil && userID != "" {
		// Active configurations
		configs, _ := repo.ListTopicCalendarConfigs(userID)
		if len(configs) > 0 {
			sb.WriteString("\n• Configured Topic Calendars:\n")
			for _, c := range configs {
				sb.WriteString(fmt.Sprintf("  - Topic `%s`: Prefix `[%s]`, Color ID `%s`\n", c.Topic, c.PrefixTag, c.ColorID))
			}
		}

		// Upcoming events in next 48 hours
		now := time.Now()
		upcoming, _ := repo.ListScheduleEvents(userID, familyGroup, "all", now, now.Add(48*time.Hour))
		if len(upcoming) > 0 {
			sb.WriteString("\n• Upcoming Topic Schedule (Next 48h):\n")
			for _, u := range upcoming {
				mode := "DB"
				if u.SyncToGoogle {
					mode = "Google"
				}
				if u.PartnerGoogleEventID != "" {
					mode += "+Partner"
				}
				sb.WriteString(fmt.Sprintf("  - [%s] %s (%s) [%s]\n", strings.ToUpper(u.Topic), u.Title, u.StartTime.Format("Mon 15:04"), mode))
			}
		}
	}

	return strings.TrimSpace(sb.String())
}

// findPartnerUsers retrieves partner users in the same family group.
func (m *Module) findPartnerUsers(callCtx orchestrator.CallContext, userID, familyGroup string) []models.User {
	var partners []models.User
	if callCtx.DB != nil && familyGroup != "" {
		users, err := callCtx.DB.GetUsersByFamilyGroup(familyGroup)
		if err == nil {
			for _, u := range users {
				if u.PlatformID != userID && u.ID != userID {
					partners = append(partners, u)
				}
			}
		}
	}
	if len(partners) == 0 {
		if partnerID, ok := callCtx.Extra["partner_user_id"].(string); ok && partnerID != "" {
			partners = append(partners, models.User{PlatformID: partnerID})
		}
	}
	return partners
}

// Helper utilities

func getStringArg(args map[string]interface{}, key string) string {
	if val, ok := args[key].(string); ok {
		return val
	}
	return ""
}

func getBoolArg(args map[string]interface{}, key string, def bool) bool {
	if val, ok := args[key].(bool); ok {
		return val
	}
	return def
}

func getIntArg(args map[string]interface{}, key string) (int64, bool) {
	if val, ok := args[key].(float64); ok {
		return int64(val), true
	}
	if val, ok := args[key].(int); ok {
		return int64(val), true
	}
	if val, ok := args[key].(int64); ok {
		return val, true
	}
	if str, ok := args[key].(string); ok {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func getStringSliceArg(args map[string]interface{}, key string) []string {
	if raw, ok := args[key].([]interface{}); ok {
		var res []string
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				res = append(res, strings.TrimSpace(s))
			}
		}
		return res
	}
	if raw, ok := args[key].([]string); ok {
		return raw
	}
	return nil
}

func hasAnyTag(eventTags, filterTags []string) bool {
	lookup := make(map[string]bool)
	for _, t := range eventTags {
		lookup[strings.ToLower(strings.TrimSpace(t))] = true
	}
	for _, f := range filterTags {
		if lookup[strings.ToLower(strings.TrimSpace(f))] {
			return true
		}
	}
	return false
}

func parseFlexibleTime(str string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, str); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time string %q", str)
}
