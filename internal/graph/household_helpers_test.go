package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
)

// Tests create tenants here (V4-30): every family label a test uses becomes a household, so the
// resolvers can build scopes from it exactly as they do in production.

// labelStore is an in-memory household store holding one household per label, for tests whose
// repositories are fakes.
func labelStore(labels ...string) *identitytest.Store {
	st := identitytest.NewStore()
	for _, l := range labels {
		st.Household(l)
	}
	return st
}

// households creates a household in the test database for each label.
func households(t *testing.T, c *db.Client, labels ...string) {
	t.Helper()
	for _, l := range labels {
		householdtest.Household(t, c, l)
	}
}

// fixtureAccess reads households from st and a person's access from the test's users row
// (identitytest.LegacyAccess), for people the test did not store as members.
type fixtureAccess struct{ identity.Store }

func (f fixtureAccess) AccessOf(_ context.Context, u *models.User) (identity.Access, error) {
	return identitytest.LegacyAccess(u), nil
}

// fixtureRole maps the role strings the tests used before V4-35 to a role: "member" (and no role)
// was every non-admin, an adult now; "admin" is an owner and the operator, as Migration C maps it.
func fixtureRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "member":
		return "family"
	}
	return role
}

// signedIn returns a context signed in to the dashboard (V4-35) as personKey with role in the
// household labelled label, created on first use; an empty label is the person's own household,
// keyed by personKey (migration 00003's rule). The households are r's: the test database, its
// in-memory store, or a fresh in-memory store when r has neither.
func signedIn(t testing.TB, r *Resolver, label, personKey, role string) context.Context {
	t.Helper()
	return WithCaller(context.Background(), scopeFor(t, r, label, personKey, role))
}

// scopeFor is the scope signedIn signs in with.
func scopeFor(t testing.TB, r *Resolver, label, personKey, role string) identity.Scope {
	t.Helper()
	if strings.TrimSpace(label) == "" {
		label = personKey
	}
	var st identity.Store
	if r != nil {
		st = r.households()
	}
	var h models.Household
	switch s := st.(type) {
	case *db.Client:
		h = householdtest.Household(t, s, label)
	case *identitytest.Store:
		h = s.Household(label)
	default:
		mem := identitytest.NewStore()
		h, st = mem.Household(label), mem
	}
	sc, err := identity.ForUser(context.Background(), fixtureAccess{st}, &models.User{PlatformID: personKey, Role: fixtureRole(role), HouseholdID: h.ID})
	if err != nil {
		t.Fatalf("scope for %q in %q: %v", personKey, label, err)
	}
	return sc
}

// stampScope does what the real repositories do on a write: the scope's household and its legacy
// label go on the row, whatever the caller put there.
func stampScope(s identity.Scope, householdID, familyGroup *string) {
	*householdID = s.HouseholdID()
	*familyGroup = s.LegacyLabel()
}
