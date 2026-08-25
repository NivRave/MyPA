package state

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) (*Store, *miniredis.Miniredis, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	store, err := NewStore("redis://" + mr.Addr())
	require.NoError(t, err)

	cleanup := func() {
		store.Close()
		mr.Close()
	}

	return store, mr, cleanup
}

func TestNewStore_InvalidURL(t *testing.T) {
	_, err := NewStore("://invalid-url")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse redis url")
}

func TestNewStore_ConnectionFailure(t *testing.T) {
	_, err := NewStore("redis://localhost:12345") // assuming nothing is on this port
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect to redis")
}

func TestAppendAndGetChatHistory(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-123"

	// Initial get should be empty
	history, err := store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, history)

	// Append 1 message
	msg1 := models.ChatMessage{Role: "user", Content: "Hello"}
	err = store.AppendChatHistory(ctx, userID, msg1)
	require.NoError(t, err)

	history, err = store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, "Hello", history[0].Content)

	// Append 25 messages (should cap at 20)
	for i := 0; i < 25; i++ {
		msg := models.ChatMessage{Role: "user", Content: "Message"}
		err = store.AppendChatHistory(ctx, userID, msg)
		require.NoError(t, err)
	}

	history, err = store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 20)
}

func TestOAuthToken(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-456"

	// Get missing token
	token, err := store.GetOAuthToken(ctx, userID)
	require.NoError(t, err)
	assert.Nil(t, token)

	// Set token
	expectedToken := []byte("some-secret-token")
	err = store.SetOAuthToken(ctx, userID, expectedToken)
	require.NoError(t, err)

	// Get token back
	token, err = store.GetOAuthToken(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, expectedToken, token)
}
