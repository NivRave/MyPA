package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"go.uber.org/mock/gomock"
)

// Tests create tenants through these helpers (V4-30). Households live in one in-memory store
// shared by the package's tests; a household's ID is derived from its label, so the same label
// is always the same household and two labels are always two households.
var testHouseholds = identitytest.NewStore()

// householdID returns the ID of the household with this label, creating it on first use.
func householdID(label string) string { return testHouseholds.Household(label).ID }

// inHousehold puts u in the household its label names — or, without a label, in a household of
// its own keyed by its platform ID (migration 00003's rule) — and returns it.
func inHousehold(u *models.User) *models.User {
	if u == nil {
		return nil
	}
	key := strings.TrimSpace(u.FamilyGroup)
	if key == "" {
		key = strings.TrimSpace(u.PlatformID)
	}
	u.HouseholdID = householdID(key)
	return u
}

// expectHouseholds lets the mock database answer household lookups from testHouseholds.
func expectHouseholds(m *mocks.MockDBClient) {
	m.EXPECT().HouseholdByID(gomock.Any(), gomock.Any()).DoAndReturn(testHouseholds.HouseholdByID).AnyTimes()
	m.EXPECT().HouseholdsByLabel(gomock.Any(), gomock.Any()).DoAndReturn(testHouseholds.HouseholdsByLabel).AnyTimes()
}

// scopeFor returns the scope of personKey with role in the household labelled label.
func scopeFor(t testing.TB, label, personKey, role string) identity.Scope {
	t.Helper()
	return testHouseholds.ScopeIn(t, label, personKey, role)
}

// idScope returns the scope of personKey in a household whose ID is the given string (its label
// too). The prompt cache treats the household ID as an opaque key segment; tests about cache keys
// use readable IDs so the keys they assert stay legible.
func idScope(id, personKey, role string) identity.Scope {
	l := id
	testHouseholds.Put(models.Household{ID: id, Name: id, LegacyLabel: &l})
	s, err := identity.ForUser(context.Background(), testHouseholds, &models.User{PlatformID: personKey, Role: role, HouseholdID: id})
	if err != nil {
		panic(err)
	}
	return s
}

// scopeOfUser returns the scope of u, placed by inHousehold's rule.
func scopeOfUser(t testing.TB, u *models.User) identity.Scope {
	t.Helper()
	key := strings.TrimSpace(u.FamilyGroup)
	if key == "" {
		key = strings.TrimSpace(u.PlatformID)
	}
	return scopeFor(t, key, u.PlatformID, u.Role)
}
