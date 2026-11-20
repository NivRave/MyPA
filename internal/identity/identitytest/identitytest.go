// Package identitytest builds scopes for tests that have no database. It goes through the same
// constructors as production, against an in-memory Store.
//
// The Store has no memberships: AccessOf reads the role from the users row a test passes, mapped
// as Migration C maps the stored roles (LegacyAccess). Production reads the membership and the
// person and ignores users.role; the db package's tests prove that path.
package identitytest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Store is an in-memory identity.Store.
type Store struct {
	mu         sync.Mutex
	households map[string]models.Household
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{households: map[string]models.Household{}} }

// HouseholdByID implements identity.Store.
func (s *Store) HouseholdByID(_ context.Context, id string) (*models.Household, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.households[id]
	if !ok {
		return nil, nil
	}
	return &h, nil
}

// AccessOf implements identity.Store from the test's users row (LegacyAccess): the household is
// the row's own.
func (s *Store) AccessOf(_ context.Context, u *models.User) (identity.Access, error) {
	return LegacyAccess(u), nil
}

// LegacyAccess is the access a test fixture's users row stands for, mapped as Migration C maps
// stored roles: "admin" is an owner and the operator, "family" an adult; the four role names stand
// for themselves and are no operator.
func LegacyAccess(u *models.User) identity.Access {
	if u == nil {
		return identity.Access{}
	}
	return identity.Access{
		HouseholdID: u.HouseholdID,
		Role:        identity.ParseRole(u.Role),
		Operator:    strings.EqualFold(strings.TrimSpace(u.Role), "admin"),
	}
}

// HouseholdsByLabel implements identity.Store.
func (s *Store) HouseholdsByLabel(_ context.Context, label string) ([]models.Household, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.Household
	for _, h := range s.households {
		if h.LegacyLabel != nil && strings.EqualFold(strings.TrimSpace(*h.LegacyLabel), strings.TrimSpace(label)) {
			out = append(out, h)
		}
	}
	return out, nil
}

// Household returns the household with this label, creating it with an ID derived from the label.
func (s *Store) Household(label string) models.Household {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := HouseholdID(label)
	if h, ok := s.households[id]; ok {
		return h
	}
	l := label
	h := models.Household{ID: id, Name: label, LegacyLabel: &l}
	s.households[id] = h
	return h
}

// Put stores a household as given.
func (s *Store) Put(h models.Household) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.households[h.ID] = h
}

// HouseholdID is the ID the Store gives the household with this label.
func HouseholdID(label string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("mypa-household:"+label)).String()
}

// Scope returns the scope of a member of the household with this label (created on first use).
// personKey is the acting person's platform ID; role is a role name, or "admin" (owner and
// operator) or "family" (adult) as stored before V4-35.
func Scope(t testing.TB, label, personKey, role string) identity.Scope {
	t.Helper()
	return (&storeHolder{NewStore()}).scope(t, label, personKey, role)
}

// ScopeIn is Scope against a given Store, so several scopes share households.
func (s *Store) ScopeIn(t testing.TB, label, personKey, role string) identity.Scope {
	t.Helper()
	return (&storeHolder{s}).scope(t, label, personKey, role)
}

type storeHolder struct{ s *Store }

func (h *storeHolder) scope(t testing.TB, label, personKey, role string) identity.Scope {
	t.Helper()
	hh := h.s.Household(label)
	sc, err := identity.ForUser(context.Background(), h.s, &models.User{PlatformID: personKey, Role: role, HouseholdID: hh.ID})
	if err != nil {
		t.Fatalf("identitytest: scope for %q: %v", label, err)
	}
	return sc
}
