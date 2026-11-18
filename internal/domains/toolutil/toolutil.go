package toolutil

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/events"
)

// String extracts a trimmed string from args map. Returns defaultVal if key is missing or empty.
func String(args map[string]interface{}, key string, defaultVal ...string) string {
	def := ""
	if len(defaultVal) > 0 {
		def = defaultVal[0]
	}
	val, ok := args[key]
	if !ok || val == nil {
		return def
	}
	switch v := val.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return def
		}
		return trimmed
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" {
			return def
		}
		return s
	}
}

// StringSlice parses []string or []interface{} into a clean []string slice.
func StringSlice(args map[string]interface{}, key string) []string {
	val, ok := args[key]
	if !ok || val == nil {
		return nil
	}
	if slice, ok := val.([]string); ok {
		return slice
	}
	if raw, ok := val.([]interface{}); ok {
		var res []string
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				res = append(res, strings.TrimSpace(s))
			}
		}
		return res
	}
	return nil
}

// Int extracts an integer from float64, int, int64, or string.
func Int(args map[string]interface{}, key string, defaultVal int) int {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return defaultVal
}

// Int64 extracts an int64 from float64, int, int64, or string.
func Int64(args map[string]interface{}, key string, defaultVal int64) int64 {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

// Uint extracts an unsigned integer from uint, int, int64, float64, or string.
func Uint(args map[string]interface{}, key string, defaultVal uint) uint {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case uint:
		return v
	case int:
		if v >= 0 {
			return uint(v)
		}
	case int64:
		if v >= 0 {
			return uint(v)
		}
	case float64:
		if v >= 0 {
			return uint(v)
		}
	case string:
		if u, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			return uint(u)
		}
	}
	return defaultVal
}

// Float64 extracts a float64 from float64, float32, int, int64, or string.
func Float64(args map[string]interface{}, key string, defaultVal float64) float64 {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
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
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return defaultVal
}

// Bool extracts a boolean from bool, string ("true", "1", "yes"), int, or float64.
func Bool(args map[string]interface{}, key string, defaultVal bool) bool {
	val, ok := args[key]
	if !ok || val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		if lower == "true" || lower == "1" || lower == "yes" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "no" {
			return false
		}
	case int:
		return v != 0
	case float64:
		return v != 0
	}
	return defaultVal
}

// ParseTimeInLocation parses a date/time string in the specified timezone location.
// If loc is nil, UTC is used. RFC3339 timestamps with explicit zone offsets respect their offset,
// whereas naive date/time strings ("2006-01-02 15:04:05") are interpreted in loc before converting to UTC.
func ParseTimeInLocation(str string, loc *time.Location) (time.Time, error) {
	s := strings.TrimSpace(str)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	if loc == nil {
		loc = time.UTC
	}

	// 1. If string contains timezone/offset (Z or +03:00), parse directly via RFC3339
	if strings.HasSuffix(s, "Z") || strings.Contains(s, "+") || (strings.Count(s, "-") > 2) {
		for _, f := range []string{time.RFC3339, time.RFC3339Nano} {
			if t, err := time.Parse(f, s); err == nil {
				return t.UTC(), nil
			}
		}
	}

	// 2. Parse against formats in the given location
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time string %q in location %v", str, loc)
}

// ParseTime parses a flexible date/time string against common ISO 8601 / RFC 3339 formats.
func ParseTime(str string) (time.Time, error) {
	return ParseTimeInLocation(str, time.UTC)
}

// Time extracts a time.Time from args at key. Returns (nil, nil) if key is missing or empty.
// If an optional *time.Location is provided, naive timestamps are parsed in that location before conversion to UTC.
func Time(args map[string]interface{}, key string, loc ...*time.Location) (*time.Time, error) {
	val := String(args, key)
	if val == "" {
		return nil, nil
	}
	l := time.UTC
	if len(loc) > 0 && loc[0] != nil {
		l = loc[0]
	}
	t, err := ParseTimeInLocation(val, l)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// FormatJSON marshals v into a standard JSON string. Returns "{}" on error.
func FormatJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// PublishMutation ensures ID and Timestamp are set, then publishes the MutationEvent to bus.
func PublishMutation(ctx context.Context, bus *events.EventBus, ev events.MutationEvent) error {
	if bus == nil {
		return nil
	}
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	return bus.Publish(ctx, ev)
}

// ParseID converts an external GraphQL or API string ID into a positive uint.
// Returns an error if the string is empty, non-numeric, or <= 0.
func ParseID(s string) (uint, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("id cannot be empty")
	}
	val, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || val == 0 {
		return 0, fmt.Errorf("invalid entity id %q: must be a positive integer", s)
	}
	return uint(val), nil
}

