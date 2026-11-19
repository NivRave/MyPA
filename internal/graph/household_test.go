package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr(s string) *string { return &s }

// V4-30: family-level settings live on the household. An admin reads and changes them for their
// own household only; the household is never an argument.
func TestHousehold_AdminUpdatesAndReadsBack(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()
	label := "fam-settings-" + uuid.NewString()
	h := householdtest.Household(t, resolver.DB, label)
	admin := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: label, TenantBound: true})

	got, err := resolver.Mutation().UpdateHousehold(admin, ptr(" The Levis "), ptr("he"), ptr("Asia/Jerusalem"), ptr("ils"))
	require.NoError(t, err)
	assert.Equal(t, h.ID, got.ID)
	assert.Equal(t, "The Levis", got.Name)
	assert.Equal(t, "he", got.Language)
	assert.Equal(t, "Asia/Jerusalem", got.TimeZone)
	assert.Equal(t, "ILS", got.Currency, "currency codes are stored upper-case")

	read, err := resolver.Query().Household(admin)
	require.NoError(t, err)
	assert.Equal(t, got, read)

	require.Eventually(t, func() bool { return len(tracker.getEvents()) == 1 }, time.Second, 10*time.Millisecond)
	ev := tracker.getEvents()[0]
	assert.Equal(t, "household", ev.Topic)
	assert.Equal(t, "update", ev.Action)
	assert.Equal(t, h.ID, ev.HouseholdID)
	assert.Equal(t, label, ev.FamilyGroup)
	assert.Contains(t, ev.AfterJSON, "Asia/Jerusalem")

	// Omitted fields stay; an empty string resets a setting to the install default.
	got, err = resolver.Mutation().UpdateHousehold(admin, nil, nil, ptr(""), nil)
	require.NoError(t, err)
	assert.Equal(t, "The Levis", got.Name)
	assert.Equal(t, "ILS", got.Currency)
	assert.Empty(t, got.TimeZone)
}

func TestHousehold_FamilyMemberMayNotChangeSettings(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	label := "fam-member-" + uuid.NewString()
	householdtest.Household(t, resolver.DB, label)
	member := WithClaims(context.Background(), &AuthClaims{UserID: "omer", Role: "family", FamilyGroup: label})

	_, err := resolver.Mutation().UpdateHousehold(member, nil, nil, ptr("Asia/Tokyo"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")

	read, err := resolver.Query().Household(member)
	require.NoError(t, err, "a member may read their household")
	assert.Empty(t, read.TimeZone, "the refused change was not applied")
}

func TestHousehold_InvalidSettingsAreRefused(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	label := "fam-invalid-" + uuid.NewString()
	householdtest.Household(t, resolver.DB, label)
	admin := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: label, TenantBound: true})

	cases := map[string][4]*string{
		"blank name":          {ptr("   "), nil, nil, nil},
		"name too long":       {ptr(strings.Repeat("א", 101)), nil, nil, nil},
		"unknown time zone":   {nil, nil, ptr("Mars/Olympus"), nil},
		"local time zone":     {nil, nil, ptr("Local"), nil},
		"short currency":      {nil, nil, nil, ptr("US")},
		"long currency":       {nil, nil, nil, ptr("EURO")},
		"digit in currency":   {nil, nil, nil, ptr("U5D")},
		"language with space": {nil, ptr("he il"), nil, nil},
		"language too long":   {nil, ptr(strings.Repeat("a", 36)), nil, nil},
		"language injection":  {nil, ptr("he'; DROP TABLE households; --"), nil, nil},
	}
	for name, c := range cases {
		_, err := resolver.Mutation().UpdateHousehold(admin, c[0], c[1], c[2], c[3])
		assert.Error(t, err, name)
	}
	read, err := resolver.Query().Household(admin)
	require.NoError(t, err)
	assert.Equal(t, label, read.Name)
	assert.Empty(t, read.Language)
	assert.Empty(t, read.TimeZone)
	assert.Empty(t, read.Currency)
}

// The household a caller reads or changes is always their own: the dashboard bound to household A
// never sees or changes household B.
func TestHousehold_IsAlwaysTheCallersOwn(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	labelA, labelB := "fam-a-"+uuid.NewString(), "fam-b-"+uuid.NewString()
	a := householdtest.Household(t, resolver.DB, labelA)
	b := householdtest.Household(t, resolver.DB, labelB)
	dashA := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: labelA, TenantBound: true})
	adminB := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: labelB, TenantBound: true})

	got, err := resolver.Mutation().UpdateHousehold(dashA, ptr("Household A"), nil, nil, ptr("EUR"))
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)

	readB, err := resolver.Query().Household(adminB)
	require.NoError(t, err)
	assert.Equal(t, b.ID, readB.ID)
	assert.Equal(t, labelB, readB.Name, "B's name is unchanged")
	assert.Empty(t, readB.Currency, "B's currency is unchanged")

	readA, err := resolver.Query().Household(dashA)
	require.NoError(t, err)
	assert.Equal(t, a.ID, readA.ID)
	assert.NotEqual(t, b.ID, readA.ID)
}

// ADR 0002 §9: a dashboard label is matched ignoring case, and a label that matches two households
// (labels that differ only in case stay two households after the migration) is refused on every
// path, never guessed.
func TestHousehold_LabelMatchingTwoHouseholdsIsRefused(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	suffix := uuid.NewString()
	householdtest.Household(t, resolver.DB, "Levi-"+suffix)
	householdtest.Household(t, resolver.DB, "LEVI-"+suffix)
	householdtest.Household(t, resolver.DB, "Cohen-"+suffix)
	ambiguous := "levi-" + suffix

	dash := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: "Levi-" + suffix, TenantBound: true})
	member := WithClaims(context.Background(), &AuthClaims{UserID: "omer", Role: "family", FamilyGroup: "LEVI-" + suffix})
	global := WithClaims(context.Background(), &AuthClaims{UserID: "root", Role: "admin", FamilyGroup: "Cohen-" + suffix})

	for name, ctx := range map[string]context.Context{"dashboard": dash, "member": member} {
		_, err := resolver.Query().TopicMemories(ctx, nil, nil, nil)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "matches 2 households", name)

		_, err = resolver.Mutation().SaveTopicMemory(ctx, "health", "diet", "No nuts", nil, nil, nil)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "matches 2 households", name)

		_, err = resolver.Query().Household(ctx)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "matches 2 households", name)

		_, err = resolver.Mutation().UpdateHousehold(ctx, ptr("x"), nil, nil, nil)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "matches 2 households", name)
	}

	// A global admin naming the ambiguous label is refused too.
	_, err := resolver.Query().TopicMemories(global, nil, &ambiguous, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2 households")
	_, err = resolver.Mutation().SaveTopicMemory(global, "health", "diet", "No nuts", nil, nil, &ambiguous)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2 households")

	// A member of an unambiguous household asking for the ambiguous label is simply refused.
	cohen := WithClaims(context.Background(), &AuthClaims{UserID: "dana", Role: "family", FamilyGroup: "Cohen-" + suffix})
	_, err = resolver.Query().TopicMemories(cohen, nil, &ambiguous, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")

	var n int64
	require.NoError(t, resolver.DB.DB.Table("topic_memories").Count(&n).Error)
	assert.Zero(t, n, "nothing was written by a refused call")
}
