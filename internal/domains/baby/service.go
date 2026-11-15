package baby

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strconv"
	"strings"
	"time"
)

// resolveContext extracts familyGroup and userID canonically from callCtx.
func (m *Module) resolveContext(callCtx orchestrator.CallContext) (string, string) {
	return callCtx.TenantFamilyGroup(), callCtx.ActorID()
}

// resolveBabyProfile locates a baby profile by ID, name, or auto-selection if only one baby exists.
func (m *Module) resolveBabyProfile(repo db.BabyRepository, familyGroup string, babyID uint, babyName string) (*models.BabyProfile, error) {
	if babyID > 0 {
		p, err := repo.GetBabyProfile(babyID)
		if err != nil {
			return nil, err
		}
		if p == nil || p.FamilyGroup != familyGroup {
			return nil, fmt.Errorf("baby profile with ID %d not found in family %q", babyID, familyGroup)
		}
		return p, nil
	}

	if strings.TrimSpace(babyName) != "" {
		p, err := repo.GetBabyProfileByName(familyGroup, babyName)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}

	// Auto-select if family only has 1 baby
	profiles, err := repo.GetBabyProfilesForFamily(familyGroup)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 1 {
		return &profiles[0], nil
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no baby profiles registered for family '%s'. Please create a profile first", familyGroup)
	}

	var names []string
	for _, p := range profiles {
		names = append(names, fmt.Sprintf("%s (ID: %d)", p.Name, p.ID))
	}
	return nil, fmt.Errorf("multiple babies found (%s). Please specify baby_id or baby_name", strings.Join(names, ", "))
}

// publishMutation emits a standardized MutationEvent for audit, undo, and partner notifications.
func (m *Module) publishMutation(ctx context.Context, callCtx orchestrator.CallContext, action, entityType, entityID, familyGroup, userID, summary string) {
	bus := m.bus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus == nil {
		return
	}

	ev := events.MutationEvent{
		Topic:       "baby",
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		ActorUserID: userID,
		FamilyGroup: familyGroup,
		Summary:     summary,
		Timestamp:   time.Now(),
	}
	_ = bus.Publish(ctx, ev)
}

func formatLogSummary(babyName string, log models.BabyCareLog) string {
	switch log.LogType {
	case "feeding":
		vol := ""
		if log.ValueNumeric > 0 {
			vol = fmt.Sprintf(" %.0f %s", log.ValueNumeric, log.Unit)
		}
		det := ""
		if log.Details != "" {
			det = fmt.Sprintf(" (%s)", log.Details)
		}
		return fmt.Sprintf("Logged feeding for %s:%s%s", babyName, vol, det)
	case "sleep":
		duration := ""
		if log.ValueNumeric > 0 {
			duration = fmt.Sprintf(" %.1f %s", log.ValueNumeric, log.Unit)
		}
		return fmt.Sprintf("Logged sleep for %s:%s %s", babyName, duration, log.Details)
	case "diaper":
		return fmt.Sprintf("Logged diaper change for %s: %s", babyName, log.Details)
	case "temperature":
		return fmt.Sprintf("Logged temperature check for %s: %.1f %s %s", babyName, log.ValueNumeric, log.Unit, log.Details)
	case "medication":
		return fmt.Sprintf("Logged medication for %s: %s", babyName, log.Details)
	case "milestone":
		return fmt.Sprintf("Logged milestone for %s: %s", babyName, log.Details)
	case "measurement":
		return fmt.Sprintf("Logged growth measurement for %s: %.1f %s (%s)", babyName, log.ValueNumeric, log.Unit, log.Details)
	default:
		return fmt.Sprintf("Logged %s for %s", log.LogType, babyName)
	}
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"02/01/2006",
		"01/02/2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date '%s'", s)
}

func getStringArg(args map[string]interface{}, key string) string {
	if val, ok := args[key]; ok && val != nil {
		if s, ok := val.(string); ok {
			return strings.TrimSpace(s)
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func getIntArg(args map[string]interface{}, key string) int {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			i, _ := strconv.Atoi(v)
			return i
		}
	}
	return 0
}

func getFloatArg(args map[string]interface{}, key string) float64 {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case string:
			f, _ := strconv.ParseFloat(v, 64)
			return f
		}
	}
	return 0.0
}

func getBoolArg(args map[string]interface{}, key string) bool {
	if val, ok := args[key]; ok && val != nil {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			b, _ := strconv.ParseBool(v)
			return b
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return false
}

func toJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error())
	}
	return string(b)
}
