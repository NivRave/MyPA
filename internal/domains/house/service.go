package house

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strconv"
	"strings"
	"time"
)

// getSeasonalMaintenanceSuggestions provides season-specific home maintenance upkeep templates.
func (m *Module) getSeasonalMaintenanceSuggestions(repo db.HouseRepository, familyGroup string) (string, error) {
	currentMonth := time.Now().Month()
	season := "General"
	switch currentMonth {
	case time.March, time.April, time.May:
		season = "Spring"
	case time.June, time.July, time.August:
		season = "Summer"
	case time.September, time.October, time.November:
		season = "Fall"
	case time.December, time.January, time.February:
		season = "Winter"
	}

	type suggestion struct {
		name     string
		category string
		interval int
		notes    string
		season   string
	}

	suggestions := []suggestion{
		// Spring
		{name: "AC Filter Replacement & Condenser Check", category: models.MaintenanceHVAC, interval: 90, notes: "Replace indoor air filters and clear debris around outdoor compressor", season: "Spring"},
		{name: "Roof & Gutter Inspection", category: models.MaintenanceGeneral, interval: 180, notes: "Clear winter debris, check for loose shingles and clean downspouts", season: "Spring"},
		{name: "Smoke & CO Detector Battery Test", category: models.MaintenanceGeneral, interval: 180, notes: "Press test buttons on all smoke and carbon monoxide alarms", season: "Spring"},
		// Summer
		{name: "Irrigation & Sprinkler System Check", category: models.MaintenancePlumbing, interval: 90, notes: "Inspect sprinkler heads for clogs and verify timer settings", season: "Summer"},
		{name: "Refrigerator Coil Cleaning", category: models.MaintenanceAppliance, interval: 180, notes: "Vacuum dust from behind/under refrigerator condenser coils", season: "Summer"},
		{name: "Pest Control Perimeter Spray", category: models.MaintenancePestControl, interval: 180, notes: "Inspect foundations and apply barrier pest prevention", season: "Summer"},
		// Fall
		{name: "Boiler & Heating System Tune-Up", category: models.MaintenanceHVAC, interval: 365, notes: "Schedule certified technician inspection before cold weather", season: "Fall"},
		{name: "Chimney & Fireplace Inspection", category: models.MaintenanceGeneral, interval: 365, notes: "Check flue damper and have chimney swept if wood-burning", season: "Fall"},
		{name: "Exterior Pipe & Hose Bib Winterization", category: models.MaintenancePlumbing, interval: 365, notes: "Drain outdoor spigots and insulate exposed pipes to prevent freezing", season: "Fall"},
		// Winter
		{name: "Water Heater Flush & Descaling", category: models.MaintenancePlumbing, interval: 365, notes: "Drain sediment from tank or descale tankless heat exchanger", season: "Winter"},
		{name: "Dryer Vent Exhaust Cleaning", category: models.MaintenanceAppliance, interval: 180, notes: "Disconnect vent hose and vacuum lint buildup to prevent fire hazard", season: "Winter"},
		{name: "Door & Window Weatherstripping Check", category: models.MaintenanceGeneral, interval: 365, notes: "Inspect draft seals and replace worn weatherstripping", season: "Winter"},
	}

	// Fetch existing to indicate which tasks are already added
	existingItems, _ := repo.ListMaintenanceItems(familyGroup, "all", false)
	existingMap := make(map[string]bool)
	for _, it := range existingItems {
		existingMap[strings.ToLower(strings.TrimSpace(it.TaskName))] = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Seasonal Home Maintenance Suggestions (Current Season: %s)\n", season))
	sb.WriteString("Regular preventive upkeep keeps home systems operating efficiently and prevents costly repairs.\n\n")

	for _, s := range suggestions {
		isCurrentSeason := s.season == season
		badge := fmt.Sprintf("[%s]", s.season)
		if isCurrentSeason {
			badge = fmt.Sprintf("🌟 **[%s RECOMMENDED NOW]**", s.season)
		}

		alreadyAdded := existingMap[strings.ToLower(s.name)]
		statusNote := "➕ Recommended"
		if alreadyAdded {
			statusNote = "✅ Already in your maintenance list"
		}

		sb.WriteString(fmt.Sprintf("- %s **%s** (%s, every %d days) — *%s*\n", badge, s.name, strings.ToUpper(s.category), s.interval, statusNote))
		sb.WriteString(fmt.Sprintf("  • Upkeep Tip: %s\n", s.notes))
	}

	sb.WriteString("\n*To add any of these tasks, tell me: 'Add [Task Name] every [Interval] days under [Category]'*")

	return sb.String(), nil
}

// publishEvent sends a MutationEvent to the configured event bus.
func (m *Module) publishEvent(ctx context.Context, callCtx orchestrator.CallContext, event events.MutationEvent) {
	bus := m.bus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus == nil {
		return
	}

	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Topic == "" {
		event.Topic = "house"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	_ = bus.Publish(ctx, event)
}

func resolveFamilyGroup(callCtx orchestrator.CallContext) string {
	return callCtx.TenantFamilyGroup()
}

func resolveUserID(callCtx orchestrator.CallContext) string {
	return callCtx.ActorID()
}

func parseDate(v interface{}) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"02/01/2006",
		"01/02/2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("cannot parse date: %s", s)
}

func parseFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(val), 64)
		return f
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

func parseInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case float32:
		return int(val)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(val))
		return i
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	default:
		return 0
	}
}

func parseBool(v interface{}, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(val))
		if err != nil {
			return defaultVal
		}
		return b
	default:
		return defaultVal
	}
}

func parseKeyTerms(v interface{}) string {
	if v == nil {
		return "{}"
	}
	switch val := v.(type) {
	case string:
		val = strings.TrimSpace(val)
		if val == "" {
			return "{}"
		}
		// If valid JSON, keep as is
		var js map[string]interface{}
		if err := json.Unmarshal([]byte(val), &js); err == nil {
			return val
		}
		// Wrap string in JSON terms
		b, _ := json.Marshal(map[string]string{"summary": val})
		return string(b)
	case map[string]interface{}:
		b, err := json.Marshal(val)
		if err != nil {
			return "{}"
		}
		return string(b)
	default:
		return "{}"
	}
}
