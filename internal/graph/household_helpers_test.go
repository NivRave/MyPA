package graph

import (
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
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

// stampScope does what the real repositories do on a write: the scope's household and its legacy
// label go on the row, whatever the caller put there.
func stampScope(s identity.Scope, householdID, familyGroup *string) {
	*householdID = s.HouseholdID()
	*familyGroup = s.LegacyLabel()
}
