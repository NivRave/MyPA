package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nivik/mypa/internal/quiet"
	"gorm.io/gorm"
)

// QuietSettings returns what is needed to read one person's clock (V4-05): the time zone of the
// household of their users row and the quiet hours in their preferences. userID is the person
// key reminders, workflows and rules carry (the platform ID of their users row). A person with no
// users row or no preferences gets empty fields (the install zone, no quiet hours), not an error.
func (c *Client) QuietSettings(ctx context.Context, userID string) (quiet.Settings, error) {
	var s quiet.Settings
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return s, nil
	}
	u, err := c.GetUser(userID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
	case err != nil:
		return s, fmt.Errorf("quiet settings: %w", err)
	case strings.TrimSpace(u.HouseholdID) != "":
		h, herr := c.HouseholdByID(ctx, u.HouseholdID)
		if herr != nil {
			return s, fmt.Errorf("quiet settings: %w", herr)
		}
		if h != nil {
			s.TimeZone = h.TimeZone
		}
	}
	pref, err := c.GetUserPreference(userID)
	if err != nil {
		return s, fmt.Errorf("quiet settings: %w", err)
	}
	if pref != nil {
		s.Start, s.End = pref.QuietHoursStart, pref.QuietHoursEnd
	}
	return s, nil
}
