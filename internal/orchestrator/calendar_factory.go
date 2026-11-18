package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nivik/mypa/internal/calendar"
	"golang.org/x/oauth2"
)

// NewCalendarFactory builds the per-user Google Calendar client factory shared by the engine and
// the calendar-aware modules. It returns ErrCalendarNotConnected only when the user has no stored
// token; a token that cannot be read or decoded is a wrapped error, so callers can log it and tell
// the user something is wrong instead of treating it as "never connected" (BL-1).
func NewCalendarFactory(getToken func(context.Context, string) ([]byte, error), oauth *calendar.OAuthConfig) func(context.Context, string) (CalendarClient, error) {
	return func(ctx context.Context, userID string) (CalendarClient, error) {
		tokenBytes, err := getToken(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("read google token: %w", err)
		}
		if len(tokenBytes) == 0 {
			return nil, ErrCalendarNotConnected
		}
		var tok oauth2.Token
		if err := json.Unmarshal(tokenBytes, &tok); err != nil {
			return nil, fmt.Errorf("decode google token: %w", err)
		}
		return calendar.NewClient(ctx, oauth.TokenSource(ctx, &tok))
	}
}
