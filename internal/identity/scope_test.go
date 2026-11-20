package identity_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR 0002 Decision 1: one role-to-action table, with a test that walks every pair (V4-35): four
// household roles, each with and without the operator flag. The operator flag adds the install's
// commands and the sign-in link, never household content.
func TestCan_WalksEveryRoleAndActionPair(t *testing.T) {
	owner := map[identity.Action]bool{identity.UseAssistant: true, identity.SignInDashboard: true, identity.UseDashboard: true,
		identity.ManageHousehold: true, identity.ManageMembers: true, identity.ViewMemberActivity: true}
	adult := map[identity.Action]bool{identity.UseAssistant: true, identity.SignInDashboard: true, identity.UseDashboard: true}
	chatOnly := map[identity.Action]bool{identity.UseAssistant: true}
	want := map[identity.Role]map[identity.Action]bool{
		identity.RoleOwner: owner, identity.RoleAdult: adult, identity.RoleChild: chatOnly, identity.RoleGuest: chatOnly,
	}
	operatorAdds := map[identity.Action]bool{identity.RunBackup: true, identity.RestoreBackup: true, identity.RunDoctor: true, identity.SignInDashboard: true}

	require.Len(t, want, len(identity.Roles), "every role has a row")
	require.Len(t, identity.Actions, 9, "a new action needs a column here")
	for _, r := range identity.Roles {
		for _, operator := range []bool{false, true} {
			st := identitytest.NewStore()
			h := st.Household("Levi")
			s, err := identity.ForUser(context.Background(), accessStore{st, identity.Access{HouseholdID: h.ID, Role: r, Operator: operator}},
				&models.User{ID: "p-1", PlatformID: "1001", HouseholdID: h.ID})
			require.NoError(t, err)
			assert.Equal(t, operator, s.Operator())
			for _, a := range identity.Actions {
				assert.Equal(t, want[r][a] || (operator && operatorAdds[a]), s.Can(a), "role %s, operator %v, action %s", r, operator, a)
			}
		}
	}
	// No role (an unknown stored string) may do nothing, unless it is the operator.
	st := identitytest.NewStore()
	h := st.Household("Levi")
	s, err := identity.ForUser(context.Background(), accessStore{st, identity.Access{HouseholdID: h.ID}}, &models.User{ID: "p-1", HouseholdID: h.ID})
	require.NoError(t, err)
	for _, a := range identity.Actions {
		assert.False(t, s.Can(a), "no role, action %s", a)
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

// Stored roles map as Migration C maps them; an unknown string is no role at all.
func TestParseRole(t *testing.T) {
	cases := map[string]identity.Role{
		"owner": identity.RoleOwner, " Owner ": identity.RoleOwner, "admin": identity.RoleOwner, " Admin ": identity.RoleOwner,
		"adult": identity.RoleAdult, "family": identity.RoleAdult, "child": identity.RoleChild, "GUEST": identity.RoleGuest,
	}
	for in, want := range cases {
		assert.Equal(t, want, identity.ParseRole(in), "role %q", in)
	}
	for _, s := range []string{"", "member", "administrator", "operator", "admin; DROP TABLE users", "*"} {
		assert.Equal(t, identity.Role(""), identity.ParseRole(s), "role %q", s)
	}
}

// A pairing code gives adult, child or guest, never owner (ADR 0002 Decision 5).
func TestParseInviteRole_NeverGivesOwner(t *testing.T) {
	for in, want := range map[string]identity.Role{"adult": identity.RoleAdult, "child": identity.RoleChild, "guest": identity.RoleGuest,
		"family": identity.RoleAdult, "owner": identity.RoleAdult, "admin": identity.RoleAdult} {
		got, ok := identity.ParseInviteRole(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "operator", "root"} {
		_, ok := identity.ParseInviteRole(in)
		assert.False(t, ok, in)
	}
}

// users.role keeps the two values the release before V4 reads.
func TestLegacyRole(t *testing.T) {
	assert.Equal(t, "admin", identity.LegacyRole(identity.RoleOwner))
	for _, r := range []identity.Role{identity.RoleAdult, identity.RoleChild, identity.RoleGuest, ""} {
		assert.Equal(t, "family", identity.LegacyRole(r), r)
	}
}

// accessStore answers AccessOf with a fixed access, as a membership would, whatever the users
// row's role says.
type accessStore struct {
	*identitytest.Store
	acc identity.Access
}

func (a accessStore) AccessOf(context.Context, *models.User) (identity.Access, error) {
	return a.acc, nil
}

type noAccess struct{ *identitytest.Store }

func (noAccess) AccessOf(context.Context, *models.User) (identity.Access, error) {
	return identity.Access{}, fmt.Errorf("%w: no membership", identity.ErrNoScope)
}

// V4-35: the role and the operator flag come from the store (the membership and the person), not
// from users.role; a person without a membership, or whose membership names another household,
// has no scope.
func TestForUser_TakesTheRoleFromTheMembership(t *testing.T) {
	st := identitytest.NewStore()
	levi, cohen := st.Household("Levi"), st.Household("Cohen")
	ctx := context.Background()
	row := &models.User{ID: "p-1", PlatformID: "1001", Role: "admin", HouseholdID: levi.ID}

	s, err := identity.ForUser(ctx, accessStore{st, identity.Access{HouseholdID: levi.ID, Role: identity.RoleGuest}}, row)
	require.NoError(t, err)
	assert.Equal(t, identity.RoleGuest, s.Role(), "users.role says admin; the membership says guest")
	assert.False(t, s.Operator())
	assert.False(t, s.Can(identity.RunBackup))

	_, err = identity.ForUser(ctx, accessStore{st, identity.Access{HouseholdID: cohen.ID, Role: identity.RoleOwner}}, row)
	assert.ErrorIs(t, err, identity.ErrNoScope, "a membership of another household")

	_, err = identity.ForUser(ctx, noAccess{st}, row)
	assert.ErrorIs(t, err, identity.ErrNoScope, "no membership")
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
	assert.Equal(t, identity.RoleOwner, s.Role())
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

// A scheduled job's scope comes from the stored row and has no role: every check refuses it.
func TestForStoredRow_HasNoPowers(t *testing.T) {
	st := identitytest.NewStore()
	h := st.Household("Levi")
	s, err := identity.ForStoredRow(context.Background(), st, h.ID, "1001")
	require.NoError(t, err)
	assert.True(t, s.Valid())
	for _, a := range identity.Actions {
		assert.False(t, s.Can(a), "a job, action %s", a)
	}

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

// V4-32: the person of a scope built from a users row is that row's ID (a person keeps users.id),
// so every account of one person, and their dashboard session (V4-35), get the same person.
func TestForUser_CarriesThePerson(t *testing.T) {
	st := identitytest.NewStore()
	h := st.Household("Levi")
	s, err := identity.ForUser(context.Background(), st, &models.User{ID: "p-1", PlatformID: "7001", Role: "family", HouseholdID: h.ID})
	require.NoError(t, err)
	assert.Equal(t, "p-1", s.PersonID())
	assert.Equal(t, "7001", s.PersonKey())
}
