package db

import (
	"context"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Model usage rows (V4-04): one per chat model call. The table is person-keyed; a household's
// rows are its members', through their memberships, as for the identity tables.

// RecordModelUsage inserts the row of one model call.
func (c *Client) RecordModelUsage(ctx context.Context, u *models.ModelUsage) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if err := c.DB.WithContext(ctx).Create(u).Error; err != nil {
		return fmt.Errorf("record model usage: %w", err)
	}
	return nil
}

// TokensSince sums the tokens used since the time: the person's own, and the whole install's
// (every household, and calls outside a person's turn). An empty personID gives 0 for the person.
// It returns totals only, for the daily caps.
func (c *Client) TokensSince(ctx context.Context, personID string, since time.Time) (person, install int64, err error) {
	row := c.DB.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(input_tokens + output_tokens) FILTER (WHERE person_id = NULLIF(?, '')::uuid), 0),
		       COALESCE(SUM(input_tokens + output_tokens), 0)
		FROM model_usage WHERE created_at >= ?`, personID, since).Row()
	if err := row.Scan(&person, &install); err != nil {
		return 0, 0, fmt.Errorf("tokens since: %w", err)
	}
	return person, install, nil
}

// usageVisible is the filter of the dashboard's views: the rows of the scope's household's
// members when the scope may see member activity (an owner), else the scope's own rows.
func usageVisible(s identity.Scope) (string, []any) {
	if s.Can(identity.ViewMemberActivity) {
		return "m.household_id = ?", []any{s.HouseholdID()}
	}
	return "m.household_id = ? AND u.person_id = NULLIF(?, '')::uuid", []any{s.HouseholdID(), s.PersonID()}
}

// UsageByDay lists tokens per person and calendar day since the time, the days counted in the
// zone (an IANA name such as the install's DEFAULT_TIMEZONE), newest day first and, within a day,
// the largest total first. What the scope sees is usageVisible's.
func (c *Client) UsageByDay(ctx context.Context, s identity.Scope, since time.Time, zone string) ([]models.UsageDay, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	where, args := usageVisible(s)
	var out []models.UsageDay
	err := c.DB.WithContext(ctx).Raw(`
		SELECT u.person_id::text AS person_id, to_char(u.created_at AT TIME ZONE ?, 'YYYY-MM-DD') AS day,
		       SUM(u.input_tokens) AS input_tokens, SUM(u.output_tokens) AS output_tokens,
		       COUNT(*) AS calls, COUNT(DISTINCT NULLIF(u.turn_id, '')) AS turns
		FROM model_usage u JOIN memberships m ON m.person_id = u.person_id
		WHERE `+where+` AND u.created_at >= ?
		GROUP BY u.person_id, day
		ORDER BY day DESC, SUM(u.input_tokens + u.output_tokens) DESC, u.person_id`,
		append(append([]any{zone}, args...), since)...).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("usage by day: %w", err)
	}
	return out, nil
}

// TurnUsage lists the latest turns' tokens, newest first, at most limit. What the scope sees is
// usageVisible's.
func (c *Client) TurnUsage(ctx context.Context, s identity.Scope, limit int) ([]models.TurnUsage, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	if limit <= 0 {
		limit = 20
	}
	where, args := usageVisible(s)
	var out []models.TurnUsage
	err := c.DB.WithContext(ctx).Raw(`
		SELECT u.turn_id, u.person_id::text AS person_id, MIN(u.created_at) AS started_at,
		       SUM(u.input_tokens) AS input_tokens, SUM(u.output_tokens) AS output_tokens, COUNT(*) AS calls,
		       string_agg(DISTINCT u.provider, ',' ORDER BY u.provider) AS providers
		FROM model_usage u JOIN memberships m ON m.person_id = u.person_id
		WHERE `+where+` AND u.turn_id <> ''
		GROUP BY u.turn_id, u.person_id
		ORDER BY started_at DESC, u.turn_id
		LIMIT ?`, append(args, limit)...).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("turn usage: %w", err)
	}
	return out, nil
}
