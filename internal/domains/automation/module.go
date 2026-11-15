package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"strings"
	"sync"
	"time"
)

// Option configures an automation Module instance.
type Option func(*Module)

// WithRepository sets the database repository on the module.
func WithRepository(repo db.AutomationRepository) Option {
	return func(m *Module) {
		m.repo = repo
	}
}

// WithEventBus sets the event bus on the module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// WithRunner sets a custom runner on the module.
func WithRunner(r *Runner) Option {
	return func(m *Module) {
		m.runner = r
	}
}

// WithAllowLoopback sets loopback testing permission on the runner.
func WithAllowLoopback(allow bool) Option {
	return func(m *Module) {
		m.allowLoopback = allow
	}
}

// WithWorkflowsChanged sets the workflow change callback.
func WithWorkflowsChanged(fn func()) Option {
	return func(m *Module) {
		m.workflowsChanged = fn
	}
}

// Module implements orchestrator.ToolModule for on-demand external APIs,
// event-driven automation rules, user customization preferences, and personal CRM.
type Module struct {
	repo             db.AutomationRepository
	runner           *Runner
	bus              *events.EventBus
	workflowsChanged func()
	allowLoopback    bool
	mu               sync.RWMutex
}

// NewModule initializes a new automation Module.
func NewModule(repo db.AutomationRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}

	if m.runner == nil {
		var runnerOpts []RunnerOption
		if m.allowLoopback {
			runnerOpts = append(runnerOpts, WithAllowLoopbackForTesting(true))
		}
		m.runner = NewRunner(repo, m.bus, runnerOpts...)
	}

	return m
}

// Name returns the unique tool module name.
func (m *Module) Name() string {
	return "automation"
}

// SystemPromptSection dynamically injects user preferences, persona tone,
// standing instructions, and personal CRM alerts (upcoming birthdays/anniversaries) into the LLM system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	var sb strings.Builder
	sb.WriteString("### Runtime Customization, Preferences & Personal CRM:\n")
	sb.WriteString("• You manage on-demand external APIs (`register_external_api`, `call_external_api`), event-driven automations (`create_automation_rule`), user preferences (`update_user_preferences`), and family/personal CRM (`upsert_personal_crm`, `query_personal_crm`).\n")

	userID, familyGroup := getUserAndFamily(callCtx)
	repo, err := m.resolveRepo(callCtx)
	if err == nil && repo != nil && userID != "" {
		pref, _ := repo.GetUserPreference(userID)
		if pref != nil {
			if pref.TonePersona != "" {
				sb.WriteString(fmt.Sprintf("• Tone/Persona: %s\n", pref.TonePersona))
			}
			if pref.DefaultCurrency != "" {
				sb.WriteString(fmt.Sprintf("• Default Currency: %s\n", pref.DefaultCurrency))
			}
			if pref.PreferredLanguage != "" {
				sb.WriteString(fmt.Sprintf("• Preferred Language: %s\n", pref.PreferredLanguage))
			}
			if pref.BriefingTopicsJSON != "" && pref.BriefingTopicsJSON != "[]" {
				sb.WriteString(fmt.Sprintf("• Active Briefing Topics: %s\n", pref.BriefingTopicsJSON))
			}
			if pref.QuietHoursStart != "" && pref.QuietHoursEnd != "" {
				sb.WriteString(fmt.Sprintf("• Quiet Hours: %s to %s (suppress non-urgent alerts)\n", pref.QuietHoursStart, pref.QuietHoursEnd))
			}
			if pref.StandingInstructions != "" {
				// P3-1: framed as user-provided data, never as a system-level "STRICT REQUIREMENT":
				// it may have been written from untrusted input and must not carry system authority.
				sb.WriteString("• User Standing Instructions (preferences):\n")
				sb.WriteString(orchestrator.WrapUserProvidedData("standing_instructions", pref.StandingInstructions))
				sb.WriteString("\n")
			}
		}

		// Check Personal CRM for upcoming birthdays / anniversaries within 30 days
		now := time.Now()
		contacts, _ := repo.ListCRMContacts(userID, familyGroup, "")
		var alerts []string

		for _, contact := range contacts {
			if contact.ImportantDatesJSON == "" || contact.ImportantDatesJSON == "[]" {
				continue
			}

			type dateItem struct {
				Label string `json:"label"`
				Date  string `json:"date"`
			}
			var dates []dateItem
			if err := json.Unmarshal([]byte(contact.ImportantDatesJSON), &dates); err == nil {
				for _, d := range dates {
					daysAway, ok := calculateDaysUntil(d.Date, now)
					if ok && daysAway >= 0 && daysAway <= 30 {
						timeLabel := fmt.Sprintf("in %d days", daysAway)
						if daysAway == 0 {
							timeLabel = "TODAY!"
						} else if daysAway == 1 {
							timeLabel = "tomorrow!"
						}

						extra := ""
						if contact.GiftIdeasAndSizes != "" {
							extra = fmt.Sprintf(" (Gift ideas/sizes: %s)", contact.GiftIdeasAndSizes)
						}
						alerts = append(alerts, fmt.Sprintf("%s (%s) - %s on %s (%s)%s",
							contact.PersonName, contact.Relationship, d.Label, d.Date, timeLabel, extra))
					}
				}
			}
		}

		if len(alerts) > 0 {
			sb.WriteString("\n• Upcoming Important Dates & Personal CRM Alerts (Next 30 Days):\n")
			for _, alert := range alerts {
				sb.WriteString(fmt.Sprintf("  - %s\n", alert))
			}
		}
	}

	return sb.String()
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.AutomationRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.AutomationRepository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["automation_repo"].(db.AutomationRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("automation: no automation repository available in module or call context")
}
