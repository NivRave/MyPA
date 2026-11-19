package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
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
	// concurrency managed by repository
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
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
	}
	if userID == "" {
		userID = callCtx.Message.Sender
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
		upcoming, _ := repo.ListScheduleEvents(callCtx.Scope, "all", now, now.Add(48*time.Hour))
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
