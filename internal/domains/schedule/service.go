package schedule

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
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

	if userID == callerID(callCtx) {
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

// findPartnerUsers retrieves the other members of the caller's household (V4-30).
func (m *Module) findPartnerUsers(ctx context.Context, callCtx orchestrator.CallContext, userID string) []models.User {
	var partners []models.User
	if callCtx.DB != nil && callCtx.Scope.Valid() {
		users, err := callCtx.DB.UsersInHousehold(ctx, callCtx.Scope)
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
	return parseFlexibleTimeIn(str, time.UTC)
}

// parseFlexibleTimeIn reads str; a time without an offset is read in loc (V4-08).
func parseFlexibleTimeIn(str string, loc *time.Location) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, str, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time string %q", str)
}

// callerID is the user key the call acts for: the platform ID, else the user ID, else the sender.
func callerID(callCtx orchestrator.CallContext) string {
	if callCtx.User != nil {
		if callCtx.User.PlatformID != "" {
			return callCtx.User.PlatformID
		}
		if callCtx.User.ID != "" {
			return callCtx.User.ID
		}
	}
	return callCtx.Message.Sender
}

// locationArg is the event's time zone from the "timezone" argument; UTC when it is not given.
func locationArg(args map[string]interface{}) (*time.Location, error) {
	name := strings.TrimSpace(getStringArg(args, "timezone"))
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: use an IANA name such as 'Asia/Jerusalem'", name)
	}
	return loc, nil
}

// getRecurrenceArg reads "recurrence" as a list of RRULE lines or as one string.
func getRecurrenceArg(args map[string]interface{}) []string {
	if list := getStringSliceArg(args, "recurrence"); len(list) > 0 {
		return list
	}
	if s := strings.TrimSpace(getStringArg(args, "recurrence")); s != "" {
		return []string{s}
	}
	return nil
}

// splitRecurrence turns a stored RecurrenceRule back into its lines.
func splitRecurrence(rule string) []string {
	var out []string
	for _, line := range strings.Split(rule, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// eventRefArg names the event an update or delete acts on: a MyPA ID, or the Google ID of an
// event that exists only in Google. A non-numeric event_id is taken as a Google ID.
func eventRefArg(args map[string]interface{}) (uint, string, error) {
	id, isNumber := getIntArg(args, "event_id")
	if isNumber && id > 0 {
		return uint(id), "", nil
	}
	if g := strings.TrimSpace(getStringArg(args, "google_event_id")); g != "" {
		return 0, g, nil
	}
	if s := strings.TrimSpace(getStringArg(args, "event_id")); s != "" && !isNumber {
		return 0, s, nil
	}
	if isNumber {
		return 0, "", fmt.Errorf("valid event_id is required")
	}
	return 0, "", fmt.Errorf("event_id or google_event_id is required")
}

// googleEventRef is the Google event ID behind a stored reference. Rows written before V4-08 hold
// the event's web link (the client returned HtmlLink); its eid parameter is the base64 of
// "<event id> <calendar id>". Empty when the reference holds no usable ID.
func googleEventRef(stored string) string {
	stored = strings.TrimSpace(stored)
	if !strings.HasPrefix(stored, "http://") && !strings.HasPrefix(stored, "https://") {
		return stored
	}
	u, err := url.Parse(stored)
	if err != nil {
		return ""
	}
	eid := u.Query().Get("eid")
	if eid == "" {
		return ""
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if raw, err := enc.DecodeString(eid); err == nil {
			if f := strings.Fields(string(raw)); len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}

// calendarTitle is the title an event gets in Google. A plain event (topic "general", no
// configured prefix) keeps its own title, as create_calendar_event always wrote it (V4-08).
func calendarTitle(ev *models.ScheduleEvent, prefixTag string) string {
	if strings.TrimSpace(prefixTag) == "" {
		if t := strings.ToLower(strings.TrimSpace(ev.Topic)); t == "" || t == generalTopic {
			return strings.TrimSpace(ev.Title)
		}
	}
	return ev.FormattedTitle(prefixTag)
}

// googleEvent builds what is sent to Google for one event, shown in loc.
func googleEvent(title, description string, start, end time.Time, loc *time.Location, recurrence []string) models.CalendarEvent {
	return models.CalendarEvent{
		Title:       title,
		Description: description,
		StartTime:   start.In(loc).Format(time.RFC3339),
		EndTime:     end.In(loc).Format(time.RFC3339),
		Timezone:    loc.String(),
		Recurrence:  recurrence,
	}
}
