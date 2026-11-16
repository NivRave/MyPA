package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strconv"
	"strings"
	"time"
)

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

// googleState records what happened with Google Calendar for one operation (BL-1), so the tool
// result can say so instead of silently presenting MyPA-only data as the full picture.
type googleState int

const (
	googleOK           googleState = iota // client available and the call succeeded
	googleOff                             // no calendar integration configured: stay silent
	googleNotConnected                    // user never ran /connect: tell them, no warning
	googleFailed                          // connected but failing: tell them and log a warning
)

const (
	noteGoogleNotConnected = "⚠️ Google Calendar is not connected, so only MyPA's own events are shown. Send /connect to include it."
	noteGoogleUnavailable  = "⚠️ Google Calendar could not be reached, so only MyPA's own events are shown. If this keeps happening, send /connect again."
)

// googleCalendar resolves userID's Google Calendar client for op. Real failures are logged here;
// a nil client comes with the reason.
func (m *Module) googleCalendar(ctx context.Context, callCtx orchestrator.CallContext, userID, op string) (orchestrator.CalendarClient, googleState) {
	client, err := m.resolveCalendarClient(ctx, callCtx, userID)
	switch {
	case err == nil && client != nil:
		return client, googleOK
	case err == nil:
		return nil, googleOff
	case errors.Is(err, orchestrator.ErrCalendarNotConnected):
		return nil, googleNotConnected
	default:
		logGoogleFailure(op, userID, err)
		return nil, googleFailed
	}
}

// logGoogleFailure records a failed Google Calendar operation. Never log event content (P3-3).
func logGoogleFailure(op, userID string, err error) {
	slog.Warn("google calendar operation failed", "op", op, "user_id", userID, "error", err)
}

// withGoogleNote appends the user-facing explanation for a degraded Google read, if any.
func withGoogleNote(result string, state googleState) string {
	switch state {
	case googleNotConnected:
		return result + "\n\n" + noteGoogleNotConnected
	case googleFailed:
		return result + "\n\n" + noteGoogleUnavailable
	default:
		return result
	}
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
