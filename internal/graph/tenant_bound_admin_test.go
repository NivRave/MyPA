package graph

import (
	"context"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P3-2 end-to-end: the dashboard (tenant-bound admin) may act on members of its own family only.
func TestTenantBoundAdmin_UserPreferenceScopedToFamily(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()

	famDash := "fam-dash-" + uuid.NewString()
	famOther := "fam-other-" + uuid.NewString()
	alice := "alice-" + uuid.NewString()
	bob := "bob-" + uuid.NewString()
	households(t, resolver.DB, "root") // the global admin's own tenant, keyed by its user ID (DE-2)
	require.NoError(t, resolver.DB.UpsertUser(models.User{PlatformID: alice, Name: "Alice", Role: "family", FamilyGroup: famDash, HouseholdID: householdtest.Household(t, resolver.DB, famDash).ID}))
	require.NoError(t, resolver.DB.UpsertUser(models.User{PlatformID: bob, Name: "Bob", Role: "family", FamilyGroup: famOther, HouseholdID: householdtest.Household(t, resolver.DB, famOther).ID}))

	dash := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: famDash, TenantBound: true})
	lang := "he"

	_, err := resolver.Mutation().SaveUserPreference(dash, alice, &lang, nil, nil, nil, nil, nil)
	require.NoError(t, err, "dashboard may manage a member of its own family")

	_, err = resolver.Mutation().SaveUserPreference(dash, bob, &lang, nil, nil, nil, nil, nil)
	assert.Error(t, err, "dashboard must not modify a user of another family")
	_, err = resolver.Query().UserPreference(dash, bob)
	assert.Error(t, err, "dashboard must not read a user of another family")

	// A global admin (JWT role admin, not tenant-bound) keeps cross-family access.
	root := WithClaims(context.Background(), &AuthClaims{UserID: "root", Role: "admin"})
	_, err = resolver.Query().UserPreference(root, bob)
	assert.NoError(t, err)
}

// Unauthenticated contexts are rejected rather than defaulting to a tenant or "system".
func TestResolvers_RejectMissingClaims(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	_, err := resolver.Mutation().RollbackMutation(context.Background(), uuid.NewString())
	assert.Error(t, err)
	_, err = resolver.Query().UserPreference(context.Background(), "anyone")
	assert.Error(t, err)
}
