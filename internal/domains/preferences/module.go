// Package preferences is the Core module for a person's preferences (language, tone, currency,
// quiet hours, briefing topics, standing instructions) and the household's personal CRM
// (V4-27). Both were part of the automation module, whose prompt section was the only way they
// reached the model; they moved here so that switching the Automations pack off cannot drop them.
package preferences

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
)

// Option configures a preferences Module.
type Option func(*Module)

// WithEventBus sets the event bus the module publishes its mutations on.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) { m.bus = bus }
}

// Module implements orchestrator.ToolModule for preferences and the personal CRM.
type Module struct {
	repo db.PreferencesRepository
	bus  *events.EventBus
}

// NewModule initializes a preferences Module.
func NewModule(repo db.PreferencesRepository, opts ...Option) *Module {
	m := &Module{repo: repo}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique tool module name.
func (m *Module) Name() string { return "preferences" }

// Execute dispatches the module's tools. Changing preferences while the turn holds untrusted
// content needs the user's confirmation, as it did in the automation module (SEC-09).
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	hasUntrusted := callCtx.HasUntrustedContent
	untrustedSources := callCtx.UntrustedSources
	if !hasUntrusted {
		if secState := orchestrator.GetTurnSecurityState(ctx); secState != nil {
			if isUntrusted, sources := secState.IsUntrusted(); isUntrusted {
				hasUntrusted = true
				untrustedSources = sources
			}
		}
	}
	if hasUntrusted && toolName == "update_user_preferences" {
		sourceDesc := "external untrusted content"
		if len(untrustedSources) > 0 {
			sourceDesc = strings.Join(untrustedSources, ", ")
		}
		return fmt.Sprintf(
			"🔒 **Interactive Security Confirmation Required**\n\n"+
				"The assistant received a command to perform a privileged system action (`%s`) while processing external data (%s).\n"+
				"To prevent indirect prompt injection, this modification was NOT applied automatically.\n\n"+
				"👉 If you intended to make this change, please reply with:\n`CONFIRM %s`",
			toolName, sourceDesc, strings.ToUpper(toolName),
		), nil
	}

	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}
	switch toolName {
	case "update_user_preferences":
		return m.executeUpdateUserPreferences(ctx, callCtx, repo, args)
	case "get_user_preferences":
		return m.executeGetUserPreferences(ctx, callCtx, repo, args)
	case "upsert_personal_crm":
		return m.executeUpsertPersonalCRM(ctx, callCtx, repo, args)
	case "query_personal_crm":
		return m.executeQueryPersonalCRM(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("preferences: unknown tool %q", toolName)
	}
}

// SystemPromptSection injects the person's preferences, persona tone and standing instructions,
// and the personal CRM's important dates in the next 30 days.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	var sb strings.Builder
	sb.WriteString("### User Preferences & Personal CRM:\n")
	sb.WriteString("• You manage user preferences (`update_user_preferences`, `get_user_preferences`) and the family/personal CRM (`upsert_personal_crm`, `query_personal_crm`).\n")

	userID, s := getUserAndScope(callCtx)
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

		// Personal CRM: upcoming birthdays / anniversaries within 30 days.
		now := time.Now()
		contacts, _ := repo.ListCRMContacts(s, "")
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
			if err := json.Unmarshal([]byte(contact.ImportantDatesJSON), &dates); err != nil {
				continue
			}
			for _, d := range dates {
				daysAway, ok := calculateDaysUntil(d.Date, now)
				if !ok || daysAway < 0 || daysAway > 30 {
					continue
				}
				timeLabel := fmt.Sprintf("in %d days", daysAway)
				switch daysAway {
				case 0:
					timeLabel = "TODAY!"
				case 1:
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
		if len(alerts) > 0 {
			sb.WriteString("\n• Upcoming Important Dates & Personal CRM Alerts (Next 30 Days):\n")
			for _, alert := range alerts {
				sb.WriteString(fmt.Sprintf("  - %s\n", alert))
			}
		}
	}
	return sb.String()
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.PreferencesRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.PreferencesRepository); ok {
		return repo, nil
	}
	return nil, fmt.Errorf("preferences: no preferences repository available in module or call context")
}

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
