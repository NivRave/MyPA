package db

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

// Tests create tenants through these helpers (V4-30): a family label becomes a household row, and
// the scope is built by the production constructor from that row. Labels are matched exactly, so
// tests that use two labels differing only in case get two households, as the migration does.

// householdFor returns the household with this exact legacy label, creating it on first use.
func householdFor(t testing.TB, c *Client, label string) models.Household {
	t.Helper()
	var h models.Household
	err := c.DB.Where("legacy_label = ?", label).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		l := label
		h = models.Household{Name: label, LegacyLabel: &l}
		if label == "" {
			// An empty label has no household of its own: it is the holding household's.
			h = models.Household{ID: models.HoldingHouseholdID, Name: "holding"}
			if err := c.DB.Where("id = ?", h.ID).FirstOrCreate(&h).Error; err != nil {
				t.Fatalf("holding household: %v", err)
			}
			return h
		}
		if err := c.DB.Create(&h).Error; err != nil {
			t.Fatalf("create household %q: %v", label, err)
		}
		return h
	}
	if err != nil {
		t.Fatalf("find household %q: %v", label, err)
	}
	return h
}

// fixtureAccess reads households from the database and a person's access from the test's users
// row, as Migration C maps stored roles, for scopes of people the test never stored. The real
// AccessOf (memberships and people) is tested in access_repo_test.go.
type fixtureAccess struct{ *Client }

func (f fixtureAccess) AccessOf(_ context.Context, u *models.User) (identity.Access, error) {
	role, operator := entryRole(u.Role)
	return identity.Access{HouseholdID: u.HouseholdID, Role: role, Operator: operator}, nil
}

// scopeAs returns the scope of personKey with role in the household labelled label. A label
// that cannot have a scope (the empty one) yields the zero, invalid scope, which every
// repository method refuses.
func scopeAs(t testing.TB, c *Client, label, personKey, role string) identity.Scope {
	t.Helper()
	h := householdFor(t, c, label)
	s, err := identity.ForUser(context.Background(), fixtureAccess{c}, &models.User{PlatformID: personKey, Role: role, HouseholdID: h.ID})
	if err != nil {
		if errors.Is(err, identity.ErrNoScope) {
			return identity.Scope{}
		}
		t.Fatalf("scope for %q: %v", label, err)
	}
	return s
}

// scopeFor is scopeAs for a family member with a fixed person key.
func scopeFor(t testing.TB, c *Client, label string) identity.Scope {
	t.Helper()
	return scopeAs(t, c, label, "test-user", "family")
}
