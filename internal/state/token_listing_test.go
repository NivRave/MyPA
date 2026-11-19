package state

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthTokenUserIDs_ListsOnlyTokenKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	ids, err := store.OAuthTokenUserIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ids)

	require.NoError(t, store.SetOAuthToken(ctx, "1002", []byte(`{"access_token":"b"}`)))
	require.NoError(t, store.SetOAuthToken(ctx, "+12025550101", []byte(`{"access_token":"a"}`)))
	// Keys that merely look similar must not be listed.
	require.NoError(t, mr.Set("legacy-token:1003", "x"))
	require.NoError(t, mr.Set("oauth_state:abc", "1004"))
	require.NoError(t, mr.Set("tokens", "x"))

	ids, err = store.OAuthTokenUserIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"+12025550101", "1002"}, ids)
}
