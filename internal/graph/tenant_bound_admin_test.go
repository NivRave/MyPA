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

// P3-2 end-to-end, as of V4-35: before V4-35 the dashboard (a tenant-bound admin) could change the
// preferences of a member of its own family and a global admin anyone's. Since V4-35 an owner
// sees and changes only their own personal data (ADR 0002 Decision 1): a request naming another
// member, in the household or outside it, acts on the caller's own preferences.
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

	dash := signedIn(t, resolver, famDash, "dashboard-service", "admin")
	lang := "he"

	for _, target := range []string{alice, bob} {
		got, err := resolver.Mutation().SaveUserPreference(dash, target, &lang, nil, nil, nil, nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "dashboard-service", got.UserID, "an owner's request for %s acts on the owner's own preferences", target)
		read, err := resolver.Query().UserPreference(dash, target)
		require.NoError(t, err)
		require.NotNil(t, read)
		assert.Equal(t, "dashboard-service", read.UserID, "an owner never reads another member's preferences")
	}
	for _, member := range []string{alice, bob} {
		var n int64
		require.NoError(t, resolver.DB.DB.Table("user_preferences").Where("user_id = ?", member).Count(&n).Error)
		assert.Zero(t, n, "%s's preferences were not written by someone else", member)
	}

	// The global admin is gone: the install's operator, signed in, reads only their own.
	root := signedIn(t, resolver, "", "root", "admin")
	read, err := resolver.Query().UserPreference(root, bob)
	require.NoError(t, err)
	assert.Nil(t, read, "root has no preferences of its own; bob's are never returned")
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
