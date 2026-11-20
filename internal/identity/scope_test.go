package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR 0002 Decision 1: one role-to-action table, with a test that walks every pair. V4-30 keeps
// today's two rules: an admin may do what an admin may today (operator commands included), a
// family member the rest.
func TestCan_WalksEveryRoleAndActionPair(t *testing.T) {
	want := map[identity.Role]map[identity.Action]bool{
		identity.RoleAdmin: {
			identity.UseAssistant: true, identity.ManageHousehold: true,
			identity.RunBackup: true, identity.RestoreBackup: true, identity.RunDoctor: true,
		},
		identity.RoleFamily: {
			identity.UseAssistant: true, identity.ManageHousehold: false,
			identity.RunBackup: false, identity.RestoreBackup: false, identity.RunDoctor: false,
		},
	}
	require.Len(t, want, len(identity.Roles), "every role has a row")
	for _, r := range identity.Roles {
		require.Len(t, want[r], len(identity.Actions), "role %s: every action has a cell", r)
		s := identitytest.Scope(t, "Levi", "1001", string(r))
		for _, a := range identity.Actions {
			assert.Equal(t, want[r][a], s.Can(a), "role %s, action %s", r, a)
		}
	}
}

// A zero or holding-household scope may do nothing, whatever the role says.
func TestCan_InvalidScopeMayDoNothing(t *testing.T) {
	var zero identity.Scope
	assert.False(t, zero.Valid())
	for _, a := range identity.Actions {
		assert.False(t, zero.Can(a), "zero scope, action %s", a)
	}
}

// An unknown role string never gains admin powers; "admin" is matched trimmed, ignoring case.
func TestParseRole(t *testing.T) {
	assert.Equal(t, identity.RoleAdmin, identity.ParseRole("admin"))
	assert.Equal(t, identity.RoleAdmin, identity.ParseRole(" Admin "))
	for _, s := range []string{"", "family", "owner", "administrator", "admin; DROP TABLE users", "*"} {
		assert.Equal(t, identity.RoleFamily, identity.ParseRole(s), "role %q", s)
	}
}

func TestForUser_ReadsTheHouseholdFromTheStore(t *testing.T) {
	st := identitytest.NewStore()
	tz := "Asia/Jerusalem"
	h := st.Household("Levi")
	h.TimeZone, h.Currency, h.Language = tz, "ILS", "he"
	st.Put(h)

	s, err := identity.ForUser(context.Background(), st, &models.User{PlatformID: " 1001 ", Role: "admin", HouseholdID: h.ID, FamilyGroup: "SomethingElse"})
	require.NoError(t, err)
	assert.True(t, s.Valid())
	assert.Equal(t, h.ID, s.HouseholdID())
	assert.Equal(t, "Levi", s.LegacyLabel(), "the label comes from the household, not from the user row")
	assert.Equal(t, "1001", s.PersonKey())
	assert.Equal(t, identity.RoleAdmin, s.Role())
	assert.Equal(t, identity.Direct, s.Audience())
	assert.Equal(t, tz, s.TimeZone("UTC"))
	assert.Equal(t, "ILS", s.Currency("USD"))
	assert.Equal(t, "he", s.Language())

	// Settings left empty fall back to the install's defaults.
	plain := st.ScopeIn(t, "Cohen", "2001", "family")
	assert.Equal(t, "UTC", plain.TimeZone("UTC"))
	assert.Equal(t, "USD", plain.Currency("USD"))
	assert.Empty(t, plain.Language())
}

func TestForUser_RefusesWithoutAUsableHousehold(t *testing.T) {
	st := identitytest.NewStore()
	ctx := context.Background()
	cases := map[string]*models.User{
		"no user":                nil,
		"no household":           {PlatformID: "1"},
		"household not in store": {PlatformID: "1", HouseholdID: identitytest.HouseholdID("nowhere")},
		"holding household":      {PlatformID: "1", HouseholdID: models.HoldingHouseholdID},
	}
	st.Put(models.Household{ID: models.HoldingHouseholdID, Name: "holding"})
	for name, u := range cases {
		s, err := identity.ForUser(ctx, st, u)
		assert.ErrorIs(t, err, identity.ErrNoScope, name)
		assert.False(t, s.Valid(), name)
	}
	_, err := identity.ForUser(ctx, nil, &models.User{PlatformID: "1", HouseholdID: "x"})
	assert.ErrorIs(t, err, identity.ErrNoScope, "no store")
}

// The dashboard matches its label ignoring case, as before V4-30; a label that matches two
// households is refused rather than guessed (ADR 0002 §9).
func TestForLabel_MatchesIgnoringCaseAndRefusesAmbiguity(t *testing.T) {
	st := identitytest.NewStore()
	ctx := context.Background()
	levi := st.Household("Levi")

	s, err := identity.ForLabel(ctx, st, " levi ", "dashboard-service", identity.RoleAdmin)
	require.NoError(t, err)
	assert.Equal(t, levi.ID, s.HouseholdID())
	assert.Equal(t, "dashboard-service", s.PersonKey())

	_, err = identity.ForLabel(ctx, st, "Nobody", "x", identity.RoleAdmin)
	assert.ErrorIs(t, err, identity.ErrNoScope)
	_, err = identity.ForLabel(ctx, st, "  ", "x", identity.RoleAdmin)
	assert.ErrorIs(t, err, identity.ErrNoScope)

	st.Household("LEVI") // a second household whose label differs only in case
	_, err = identity.ForLabel(ctx, st, "Levi", "x", identity.RoleAdmin)
	assert.ErrorIs(t, err, identity.ErrNoScope, "two households match: refused")
	assert.Contains(t, err.Error(), "matches 2 households")
}

// A scheduled job's scope comes from the stored row and has no admin powers.
func TestForStoredRow_HasNoAdminPowers(t *testing.T) {
	st := identitytest.NewStore()
	h := st.Household("Levi")
	s, err := identity.ForStoredRow(context.Background(), st, h.ID, "1001")
	require.NoError(t, err)
	assert.True(t, s.Can(identity.UseAssistant))
	assert.False(t, s.Can(identity.ManageHousehold))
	assert.False(t, s.Can(identity.RunBackup))

	_, err = identity.ForStoredRow(context.Background(), st, "", "1001")
	assert.ErrorIs(t, err, identity.ErrNoScope)
}

type members []models.User

func (m members) UsersInHousehold(_ context.Context, s identity.Scope) ([]models.User, error) {
	var out []models.User
	for _, u := range m {
		if u.HouseholdID == s.HouseholdID() {
			out = append(out, u)
		}
	}
	return out, nil
}

// ADR 0002 Decision 3: an argument that names another person is resolved inside the caller's
// household only, and a miss is an error.
func TestMember_LooksOnlyInsideTheCallersHousehold(t *testing.T) {
	st := identitytest.NewStore()
	levi, cohen := st.Household("Levi"), st.Household("Cohen")
	m := members{
		{ID: "u-dana", PlatformID: "1001", HouseholdID: levi.ID},
		{ID: "u-omer", PlatformID: "1002", HouseholdID: levi.ID},
		{ID: "u-stranger", PlatformID: "2001", HouseholdID: cohen.ID},
	}
	s := st.ScopeIn(t, "Levi", "1001", "family")
	ctx := context.Background()

	got, err := identity.Member(ctx, m, s, "1002")
	require.NoError(t, err)
	assert.Equal(t, "1002", got)
	got, err = identity.Member(ctx, m, s, "u-omer")
	require.NoError(t, err)
	assert.Equal(t, "1002", got, "a users.id resolves to the member's platform ID")
	got, err = identity.Member(ctx, m, s, " 1001 ")
	require.NoError(t, err)
	assert.Equal(t, "1001", got, "the caller is a member of their own household")

	for _, ref := range []string{"2001", "u-stranger", "", "*", "9999", "' OR 1=1 --"} {
		_, err := identity.Member(ctx, m, s, ref)
		assert.True(t, errors.Is(err, identity.ErrNotAMember), "ref %q must not resolve", ref)
	}
	_, err = identity.Member(ctx, m, identity.Scope{}, "1002")
	assert.ErrorIs(t, err, identity.ErrNoScope)
}
