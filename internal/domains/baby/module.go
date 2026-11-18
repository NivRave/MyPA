package baby

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"strings"
	"time"
)

// Option defines a functional option for Module configuration.
type Option func(*Module)

// WithEventBus configures an EventBus for mutation event publishing.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for the Baby & Family Care Tracker.
type Module struct {
	repo db.BabyRepository
	bus  *events.EventBus
	// concurrency managed by repository
}

// NewModule constructs a new baby ToolModule.
func NewModule(repo db.BabyRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name returns the unique identifier for this tool module.
func (m *Module) Name() string {
	return "baby"
}

// resolveRepo gets the active BabyRepository.
func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.BabyRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.BabyRepository); ok {
		return repo, nil
	}
	return nil, fmt.Errorf("no baby repository configured")
}

// SystemPromptSection injects current baby context, age, milestones, and medication status.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return ""
	}

	familyGroup, _ := m.resolveContext(callCtx)
	profiles, err := repo.GetBabyProfilesForFamily(familyGroup)
	if err != nil || len(profiles) == 0 {
		return ""
	}

	now := time.Now()
	var b strings.Builder
	b.WriteString("\n### Baby & Family Care Tracker (Active Lifecycle):\n")

	for _, p := range profiles {
		age := CalculateAge(p.DateOfBirth, p.DueDate, now)
		b.WriteString(fmt.Sprintf("- **%s**: DOB %s | Current Age: %s", p.Name, p.DateOfBirth.Format("2006-01-02"), age.FormattedAge))
		if age.IsPremature && age.AdjustedFormatted != "" {
			b.WriteString(fmt.Sprintf(" (%s)", age.AdjustedFormatted))
		}
		b.WriteString("\n")

		// Developmental Leap
		leap, status := GetDevelopmentalLeapStatus(age.Weeks)
		if leap != nil && (status == "active" || status == "upcoming") {
			b.WriteString(fmt.Sprintf("  - Milestone Leap: Leap %d (%s) - %s (Peaks week %d)\n", leap.LeapNumber, leap.Title, strings.ToUpper(status), leap.PeakWeek))
		}

		// Medication Cooldown
		lastMed, _ := repo.GetLastMedicationLog(p.ID, familyGroup, "")
		if lastMed != nil && now.Sub(lastMed.Timestamp) < 24*time.Hour {
			cd := CheckMedicationCooldown(lastMed, lastMed.Details, now, age.Months)
			if !cd.IsSafeToAdminister {
				b.WriteString(fmt.Sprintf("  - ⚠️ Medication Cooldown: %s given at %s (Only %s ago). Next safe dose after %s (in %s).\n",
					cd.MedicationName, lastMed.Timestamp.Format("15:04"), cd.ElapsedFormatted, cd.NextSafeDoseAt.Format("15:04"), cd.TimeUntilFormatted))
			} else {
				b.WriteString(fmt.Sprintf("  - Medication Status: %s last given %s ago at %s (Safe to administer if needed).\n",
					cd.MedicationName, cd.ElapsedFormatted, lastMed.Timestamp.Format("15:04")))
			}
		}

		// Pediatric Checkup or Vaccine Suggestion
		suggestions := GenerateMilestoneSuggestions(age, "checkup")
		for _, s := range suggestions {
			if s.Urgency == "due_now" {
				b.WriteString(fmt.Sprintf("  - 🏥 Pediatric Checkup Due: %s (%s)\n", s.Title, s.Description))
				break
			}
		}
	}

	// Inventory alerts
	lowItems, _ := repo.GetLowBabyInventoryItems(familyGroup)
	if len(lowItems) > 0 {
		var itemAlerts []string
		for _, it := range lowItems {
			itemAlerts = append(itemAlerts, fmt.Sprintf("%s (%.1f %s left, threshold: %.1f)", it.ItemName, it.QuantityRemaining, it.Unit, it.LowThreshold))
		}
		b.WriteString(fmt.Sprintf("- ⚠️ Low Baby Supplies: %s\n", strings.Join(itemAlerts, "; ")))
	}

	return b.String()
}
