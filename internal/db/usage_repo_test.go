package db

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func usageRow(personID, turnID, provider string, in, out int64, at time.Time) *models.ModelUsage {
	var p *string
	if personID != "" {
		p = &personID
	}
	return &models.ModelUsage{PersonID: p, TurnID: turnID, Provider: provider, Model: provider + "-model",
		InputTokens: in, OutputTokens: out, CreatedAt: at}
}

// V4-04: each model call is a row; the caps read today's tokens of the person and of the whole
// install, from the start of the day on.
func TestModelUsage_TokensSince(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	alice, _ := personWithAccount(t, c, "Levi", "8001", "Alice", "admin", "telegram", now)
	bob, _ := personWithAccount(t, c, "Cohen", "8002", "Bob", "admin", "telegram", now)

	for _, u := range []*models.ModelUsage{
		usageRow(alice.ID, "turn-1", "gemini", 100, 10, now.Add(-time.Minute)),
		usageRow(alice.ID, "turn-1", "gemini", 200, 20, now.Add(-time.Minute)),
		usageRow(alice.ID, "turn-0", "gemini", 5000, 500, now.Add(-30*time.Hour)), // yesterday
		usageRow(bob.ID, "turn-2", "anthropic", 1000, 50, now.Add(-time.Minute)),
		usageRow("", "", "gemini", 7, 3, now.Add(-time.Minute)), // a call outside a person's turn
	} {
		require.NoError(t, c.RecordModelUsage(ctx, u))
		require.NotZero(t, u.ID)
	}

	since := now.Add(-time.Hour)
	person, install, err := c.TokensSince(ctx, alice.ID, since)
	require.NoError(t, err)
	assert.EqualValues(t, 330, person, "Alice's two calls today")
	assert.EqualValues(t, 330+1050+10, install, "every call today, the one without a person too")

	person, install, err = c.TokensSince(ctx, "", since)
	require.NoError(t, err)
	assert.EqualValues(t, 0, person, "no person, no person total")
	assert.EqualValues(t, 1390, install)
}

// A removed person's tokens still count for the install that day; a merged person's follow the
// survivor, so linking two accounts does not reset anyone's cap.
func TestModelUsage_RemovedAndMergedPeople(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	now := time.Now()

	gone, _ := personWithAccount(t, c, "Levi", "8101", "Gone", "family", "telegram", old)
	require.NoError(t, c.RecordModelUsage(ctx, usageRow(gone.ID, "t", "gemini", 40, 2, now)))
	require.NoError(t, c.DB.Exec(`DELETE FROM memberships WHERE person_id = ?`, gone.ID).Error)
	require.NoError(t, c.DB.Exec(`DELETE FROM channel_accounts WHERE person_id = ?`, gone.ID).Error)
	require.NoError(t, c.DB.Exec(`DELETE FROM users WHERE id = ?`, gone.ID).Error)
	require.NoError(t, c.DB.Exec(`DELETE FROM people WHERE id = ?`, gone.ID).Error)
	_, install, err := c.TokensSince(ctx, gone.ID, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 42, install, "the install's total keeps a removed person's calls")

	alice, tg := personWithAccount(t, c, "Levi", "8102", "Alice", "family", "telegram", old)
	alias, wa := personWithAccount(t, c, "Levi", "+15550008103", "Alice WhatsApp", "family", "whatsapp", old)
	require.NoError(t, c.RecordModelUsage(ctx, usageRow(alias.ID, "t2", "gemini", 900, 100, now)))
	hash := identity.HashCode(testCodeKey, "UUUU7777")
	require.NoError(t, c.IssueLinkCode(ctx, scopeOfUser(t, c, alice), tg, hash))
	tok, err := c.ClaimLinkCode(ctx, hash, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	_, err = c.ConfirmMergeByIssuer(ctx, tok.ID, tg.ID)
	require.NoError(t, err)
	_, err = c.MergeOnLink(ctx, tok.ID, wa.Channel, wa.ExternalID)
	require.NoError(t, err)

	person, _, err := c.TokensSince(ctx, alice.ID, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1000, person, "the merged person's tokens are the survivor's")
}

// The dashboard's view: tokens per person and day, on the install's clock, and per turn. An
// owner sees every member of their household, an adult only themselves, and no one sees another
// household's; the zero scope is refused.
func TestModelUsage_DashboardViewsAreScoped(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	owner, _ := personWithAccount(t, c, "Levi", "8201", "Dana", "admin", "telegram", old)
	adult, _ := personWithAccount(t, c, "Levi", "8202", "Noam", "family", "telegram", old)
	other, _ := personWithAccount(t, c, "Cohen", "8203", "Ruth", "admin", "telegram", old)

	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	// 23:30 UTC on the 20th is 01:30 on the 21st in Jerusalem: the row belongs to the 21st.
	late := time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC)
	early := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	for _, u := range []*models.ModelUsage{
		usageRow(owner.ID, "turn-a", "gemini", 100, 10, early),
		usageRow(owner.ID, "turn-a", "anthropic", 50, 5, early.Add(time.Second)),
		usageRow(owner.ID, "turn-b", "gemini", 300, 30, late),
		usageRow(adult.ID, "turn-c", "gemini", 70, 7, late),
		usageRow(other.ID, "turn-d", "gemini", 9999, 999, late),
	} {
		require.NoError(t, c.RecordModelUsage(ctx, u))
	}
	since := time.Date(2026, 11, 19, 0, 0, 0, 0, jerusalem)

	ownerScope := scopeOfUser(t, c, owner)
	days, err := c.UsageByDay(ctx, ownerScope, since, jerusalem.String())
	require.NoError(t, err)
	require.Len(t, days, 3)
	assert.Equal(t, models.UsageDay{PersonID: owner.ID, Day: "2026-11-21", InputTokens: 300, OutputTokens: 30, Calls: 1, Turns: 1}, days[0])
	assert.Equal(t, models.UsageDay{PersonID: adult.ID, Day: "2026-11-21", InputTokens: 70, OutputTokens: 7, Calls: 1, Turns: 1}, days[1])
	assert.Equal(t, models.UsageDay{PersonID: owner.ID, Day: "2026-11-20", InputTokens: 150, OutputTokens: 15, Calls: 2, Turns: 1}, days[2])

	turns, err := c.TurnUsage(ctx, ownerScope, 10)
	require.NoError(t, err)
	require.Len(t, turns, 3)
	assert.Equal(t, "turn-b", turns[0].TurnID, "newest first")
	assert.Equal(t, "turn-a", turns[2].TurnID)
	assert.EqualValues(t, 150, turns[2].InputTokens)
	assert.Equal(t, 2, turns[2].Calls)
	assert.Equal(t, "anthropic,gemini", turns[2].Providers)
	assert.True(t, turns[2].StartedAt.Equal(early))
	limited, err := c.TurnUsage(ctx, ownerScope, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)

	adultScope := scopeOfUser(t, c, adult)
	days, err = c.UsageByDay(ctx, adultScope, since, "UTC")
	require.NoError(t, err)
	require.Len(t, days, 1, "an adult sees only their own usage")
	assert.Equal(t, adult.ID, days[0].PersonID)
	assert.Equal(t, "2026-11-20", days[0].Day, "in UTC the late row is still the 20th")
	turns, err = c.TurnUsage(ctx, adultScope, 10)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "turn-c", turns[0].TurnID)

	for _, rows := range [][]models.UsageDay{days} {
		for _, d := range rows {
			assert.NotEqual(t, other.ID, d.PersonID)
		}
	}
	otherDays, err := c.UsageByDay(ctx, scopeOfUser(t, c, other), since, "UTC")
	require.NoError(t, err)
	require.Len(t, otherDays, 1, "another household's owner sees only their own household")
	assert.Equal(t, other.ID, otherDays[0].PersonID)

	_, err = c.UsageByDay(ctx, identity.Scope{}, since, "UTC")
	assert.ErrorIs(t, err, identity.ErrNoScope)
	_, err = c.TurnUsage(ctx, identity.Scope{}, 10)
	assert.ErrorIs(t, err, identity.ErrNoScope)
}
