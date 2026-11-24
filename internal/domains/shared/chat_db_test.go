package shared_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-43: a topic shared with "no view" and "no edit" in chat is stored so, on create and on an
// update from true back to false.
func TestChat_SharedTopicStoresAFalseAsAsked(t *testing.T) {
	c, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	u := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	omer := householdtest.Member(t, c, "1002-"+levi, "Omer", "family", levi, "telegram")
	s, err := identity.ForUser(ctx, c, &u)
	require.NoError(t, err)
	call := orchestrator.CallContext{User: &u, Scope: s, DB: c, Extra: map[string]interface{}{}}
	mod := shared.NewModule(c, nil, nil)

	for _, want := range []bool{false, true, false} {
		_, err = mod.Execute(ctx, call, "configure_shared_topic", map[string]interface{}{
			"topic": "finance", "shared_with_user_id": omer.PlatformID, "can_view": want, "can_edit": want, "notify_mode": "silent"})
		require.NoError(t, err)
		var spaces []models.SharedSpace
		require.NoError(t, c.DB.Where("household_id = ? AND topic = ?", u.HouseholdID, "finance").Find(&spaces).Error)
		require.Len(t, spaces, 1, "one space, created then updated")
		assert.Equal(t, want, spaces[0].CanView)
		assert.Equal(t, want, spaces[0].CanEdit)
	}
}
