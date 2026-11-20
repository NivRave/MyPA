package graph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
)

// fixtureAccess reads households from st and a person's access from the test's users row
// (identitytest.LegacyAccess), for people the test did not store as members.
type fixtureAccess struct{ identity.Store }

func (f fixtureAccess) AccessOf(_ context.Context, u *models.User) (identity.Access, error) {
	return identitytest.LegacyAccess(u), nil
}

// signedIn returns a context signed in to the dashboard (V4-35) as personKey with role in the
// household labelled label (created on first use; empty: the person's own), in the test database
// c, or in an in-memory store when c is nil. Roles written before V4-35 map as Migration C maps
// them; "member" is an adult.
func signedIn(t testing.TB, c *db.Client, label, personKey, role string) context.Context {
	t.Helper()
	if strings.TrimSpace(label) == "" {
		label = personKey
	}
	if r := strings.ToLower(strings.TrimSpace(role)); r == "" || r == "member" {
		role = "family"
	}
	var st identity.Store
	var h models.Household
	if c != nil {
		st, h = c, householdtest.Household(t, c, label)
	} else {
		mem := identitytest.NewStore()
		st, h = mem, mem.Household(label)
	}
	sc, err := identity.ForUser(context.Background(), fixtureAccess{st}, &models.User{PlatformID: personKey, Role: role, HouseholdID: h.ID})
	if err != nil {
		t.Fatalf("scope for %q in %q: %v", personKey, label, err)
	}
	return graph.WithCaller(context.Background(), sc)
}
