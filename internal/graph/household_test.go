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
	admin := signedIn(t, resolver, label, "dashboard-service", "admin")

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

	// BL-47: a settings change stays off the mutation log (undo cannot roll a household back); it
	// is logged at INFO, as sign-ins and pack switches are.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, tracker.getEvents(), "a settings change publishes no mutation event")

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
	member := signedIn(t, resolver, label, "omer", "family")

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
	admin := signedIn(t, resolver, label, "dashboard-service", "admin")

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
	dashA := signedIn(t, resolver, labelA, "dashboard-service", "admin")
	adminB := signedIn(t, resolver, labelB, "dashboard-service", "admin")

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

// ADR 0002 Decision 2: labels that differ only in case stay two households after the migration,
// because merging them would join two tenants. Before V4-35 the dashboard matched its label
// ignoring case and refused a label that matched two; since V4-35 a session names its household
// by ID through the membership, so neither household can reach the other's rows.
func TestHousehold_LabelsDifferingInCaseStayTwoHouseholds(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	suffix := uuid.NewString()
	levi := householdtest.Household(t, resolver.DB, "Levi-"+suffix)
	upper := householdtest.Household(t, resolver.DB, "LEVI-"+suffix)
	require.NotEqual(t, levi.ID, upper.ID)

	dana := signedIn(t, resolver, "Levi-"+suffix, "dana", "admin")
	omer := signedIn(t, resolver, "LEVI-"+suffix, "omer", "family")

	scope := "family"
	_, err := resolver.Mutation().SaveTopicMemory(dana, "health", "diet", "Levi fact", nil, &scope)
	require.NoError(t, err)
	_, err = resolver.Mutation().SaveTopicMemory(omer, "health", "diet", "LEVI fact", nil, &scope)
	require.NoError(t, err)

	for ctx, want := range map[context.Context]string{dana: "Levi fact", omer: "LEVI fact"} {
		mems, err := resolver.Query().TopicMemories(ctx, nil, nil)
		require.NoError(t, err)
		require.Len(t, mems, 1, "each household sees only its own rows")
		assert.Equal(t, want, mems[0].Fact)
	}
	h, err := resolver.Query().Household(omer)
	require.NoError(t, err)
	assert.Equal(t, upper.ID, h.ID)
	_, err = resolver.Mutation().UpdateHousehold(omer, ptr("x"), nil, nil, nil)
	assert.ErrorContains(t, err, "only an owner", "an adult may not change the settings")
}
