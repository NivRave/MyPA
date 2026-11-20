// Package householdtest builds households and scopes in a real test database (V4-30). Tests
// create tenants through it: a family label becomes a household row, and the scope is built by
// the production constructor from that row.
package householdtest

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

// Household returns the household with this exact legacy label, creating it on first use.
func Household(t testing.TB, c *db.Client, label string) models.Household {
	t.Helper()
	var h models.Household
	err := c.DB.Where("legacy_label = ?", label).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if label == "" {
			h = models.Household{ID: models.HoldingHouseholdID, Name: "holding"}
			if err := c.DB.Where("id = ?", h.ID).FirstOrCreate(&h).Error; err != nil {
				t.Fatalf("holding household: %v", err)
			}
			return h
		}
		l := label
		h = models.Household{Name: label, LegacyLabel: &l}
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

// Scope returns the scope of personKey with role ("admin" or "family") in the household
// labelled label. The empty label yields the zero, invalid scope.
func Scope(t testing.TB, c *db.Client, label, personKey, role string) identity.Scope {
	t.Helper()
	h := Household(t, c, label)
	s, err := identity.ForUser(context.Background(), c, &models.User{PlatformID: personKey, Role: role, HouseholdID: h.ID})
	if err != nil {
		if errors.Is(err, identity.ErrNoScope) {
			return identity.Scope{}
		}
		t.Fatalf("scope for %q: %v", label, err)
	}
	return s
}

// User inserts a user in the household labelled label and returns it. A user without a label gets
// a household of their own, keyed by their platform ID, as Migration A and the bootstrap do.
func User(t testing.TB, c *db.Client, platformID, name, role, label string) models.User {
	t.Helper()
	key := label
	if key == "" {
		key = platformID
	}
	h := Household(t, c, key)
	u := models.User{PlatformID: platformID, Name: name, Role: role, FamilyGroup: label, HouseholdID: h.ID}
	if err := c.DB.Create(&u).Error; err != nil {
		t.Fatalf("create user %q: %v", platformID, err)
	}
	return u
}
