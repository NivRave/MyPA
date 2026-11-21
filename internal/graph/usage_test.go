package graph

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-04: the dashboard shows usage. An owner sees each member of their household, an adult only
// themselves, and nobody sees another household's; the install's total is the operator's only.
// The caps and the install's zone are shown to everyone.
func TestUsageQuery_ShowsUsageToEachRoleAsItMay(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	c := resolver.DB
	ctx := context.Background()
	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, jerusalem)
	resolver.UsageView = UsageSettings{PersonDaily: 1000, InstallDaily: 50_000, TimeZone: "Asia/Jerusalem", Now: func() time.Time { return now }}

	fam := "fam-usage-" + uuid.NewString()
	dana := householdtest.Member(t, c, "u-dana-"+fam, "Dana", "admin", fam, "telegram") // owner and operator
	noam := householdtest.Member(t, c, "u-noam-"+fam, "Noam", "family", fam, "telegram")
	maya := householdtest.Member(t, c, "u-maya-"+fam, "Maya", "admin", fam, "telegram")
	require.NoError(t, c.DB.Exec(`UPDATE people SET is_operator = false WHERE id = ?`, maya.ID).Error) // an owner, not the operator
	ruth := householdtest.Member(t, c, "u-ruth-"+fam, "Ruth", "admin", "other-"+fam, "telegram")

	record := func(u models.User, turn string, in, out int64, at time.Time) {
		id := u.ID
		require.NoError(t, c.RecordModelUsage(ctx, &models.ModelUsage{PersonID: &id, TurnID: turn, Provider: "gemini", Model: "m",
			InputTokens: in, OutputTokens: out, CreatedAt: at}))
	}
	record(dana, "t-dana-1", 300, 30, now.Add(-time.Hour))
	record(dana, "t-dana-0", 100, 10, now.AddDate(0, 0, -1))
	record(noam, "t-noam-1", 50, 5, now.Add(-2*time.Hour))
	record(ruth, "t-ruth-1", 9000, 900, now.Add(-time.Hour))
	record(dana, "t-dana-old", 1, 1, now.AddDate(0, 0, -40)) // outside the 14 days shown

	as := func(u models.User) context.Context {
		s, err := identity.ForUser(ctx, c, &u)
		require.NoError(t, err)
		return WithCaller(ctx, s)
	}

	// The owner and operator: every member's days and turns, the install's total.
	got, err := resolver.Query().Usage(as(dana), nil)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Jerusalem", got.TimeZone)
	assert.Equal(t, 1000, got.PersonDailyCap)
	assert.Equal(t, 50_000, got.InstallDailyCap)
	assert.Equal(t, 330, got.MyTokensToday)
	require.NotNil(t, got.InstallTokensToday)
	assert.Equal(t, 330+55+9900, *got.InstallTokensToday, "the install's total is every household's")
	require.Len(t, got.Days, 3)
	assert.Equal(t, "2026-11-21", got.Days[0].Day)
	assert.Equal(t, "Dana", got.Days[0].Name)
	assert.Equal(t, 300, got.Days[0].InputTokens)
	assert.Equal(t, "Noam", got.Days[1].Name)
	assert.Equal(t, "2026-11-20", got.Days[2].Day)
	require.Len(t, got.Turns, 4, "t-dana-old is a turn too, but turns are the latest, not by day")
	assert.Equal(t, "t-dana-1", got.Turns[0].TurnID)
	assert.Equal(t, "gemini", got.Turns[0].Providers)
	for _, d := range got.Days {
		assert.NotEqual(t, ruth.ID, d.PersonID, "another household's usage is never shown")
	}

	// An owner who is not the operator sees the members, but not the install's total.
	got, err = resolver.Query().Usage(as(maya), nil)
	require.NoError(t, err)
	assert.Nil(t, got.InstallTokensToday)
	assert.Len(t, got.Days, 3)

	// An adult sees only themselves.
	got, err = resolver.Query().Usage(as(noam), nil)
	require.NoError(t, err)
	assert.Equal(t, 55, got.MyTokensToday)
	assert.Nil(t, got.InstallTokensToday)
	require.Len(t, got.Days, 1)
	assert.Equal(t, noam.ID, got.Days[0].PersonID)
	require.Len(t, got.Turns, 1)
	assert.Equal(t, "t-noam-1", got.Turns[0].TurnID)

	// The number of days asked for is honoured, within limits.
	one := 1
	got, err = resolver.Query().Usage(as(dana), &one)
	require.NoError(t, err)
	assert.Len(t, got.Days, 2, "today only: Dana's and Noam's")
	many := 1000
	got, err = resolver.Query().Usage(as(dana), &many)
	require.NoError(t, err)
	assert.Len(t, got.Days, 4, "cut to 90 days, which reach the row of 40 days ago")

	// Not signed in: refused.
	_, err = resolver.Query().Usage(ctx, nil)
	assert.Error(t, err)
}
