package graph

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-05: quiet hours are enforced by the scheduler, which ignores a window it cannot read, so the
// dashboard refuses one instead of storing it. Clearing them (empty values) is allowed.
func TestSaveUserPreference_RefusesUnreadableQuietHours(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-quiet-" + uuid.NewString()
	ctx := signedIn(t, resolver, fam, "quiet-owner", "admin")

	late, early := "10pm", "07:00"
	_, err := resolver.Mutation().SaveUserPreference(ctx, "quiet-owner", nil, nil, nil, &late, &early, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "24-hour")
	var n int64
	require.NoError(t, resolver.DB.DB.Table("user_preferences").Where("user_id = ?", "quiet-owner").Count(&n).Error)
	assert.Zero(t, n, "nothing is stored")

	start, end := "22:00", "07:00"
	got, err := resolver.Mutation().SaveUserPreference(ctx, "quiet-owner", nil, nil, nil, &start, &end, nil)
	require.NoError(t, err)
	assert.Equal(t, "22:00", got.QuietHoursStart)

	none := ""
	got, err = resolver.Mutation().SaveUserPreference(ctx, "quiet-owner", nil, nil, nil, &none, &none, nil)
	require.NoError(t, err)
	assert.Empty(t, got.QuietHoursStart, "quiet hours can be cleared")
}
