package finance

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"strconv"
	"strings"
	"time"
)

// emitMutation dispatches a mutation event to the call context or module event bus.
func (m *Module) emitMutation(ctx context.Context, callCtx orchestrator.CallContext, event events.MutationEvent, payload interface{}) {
	bus := callCtx.EventBus
	if bus == nil {
		bus = m.bus
	}
	if bus == nil {
		return
	}

	if payload != nil {
		if bytes, err := json.Marshal(payload); err == nil {
			event.AfterJSON = string(bytes)
		}
	}
	_ = bus.Publish(ctx, event)
}

func (m *Module) resolveFamilyGroup(callCtx orchestrator.CallContext) string {
	if callCtx.User != nil && strings.TrimSpace(callCtx.User.FamilyGroup) != "" {
		return strings.TrimSpace(callCtx.User.FamilyGroup)
	}
	if fg, ok := callCtx.Extra["family_group"].(string); ok && strings.TrimSpace(fg) != "" {
		return strings.TrimSpace(fg)
	}
	return ""
}

func (m *Module) resolveUserID(callCtx orchestrator.CallContext) string {
	if callCtx.User != nil {
		return callCtx.User.ID
	}
	return ""
}

// Helpers for safe argument conversion
func parseFloat(v interface{}, defaultVal float64) float64 {
	if v == nil {
		return defaultVal
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
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return f
		}
	case string:
		clean := strings.TrimSpace(val)
		clean = strings.TrimPrefix(clean, "$")
		clean = strings.TrimPrefix(clean, "€")
		clean = strings.TrimPrefix(clean, "₪")
		if f, err := strconv.ParseFloat(clean, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func parseInt(v interface{}, defaultVal int) int {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return i
		}
	}
	return defaultVal
}

func parseString(v interface{}, defaultVal string) string {
	if v == nil {
		return defaultVal
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprintf("%v", v)
}

func parseBool(v interface{}, defaultVal bool) bool {
	if v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		lower := strings.ToLower(strings.TrimSpace(val))
		if lower == "true" || lower == "1" || lower == "yes" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "no" {
			return false
		}
	}
	return defaultVal
}

func parseDateOrDefault(v interface{}, defaultVal time.Time) time.Time {
	if v == nil {
		return defaultVal
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		if t, err := parseFlexDate(s); err == nil {
			return t
		}
	}
	return defaultVal
}

func parseFlexDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date: %s", s)
}
