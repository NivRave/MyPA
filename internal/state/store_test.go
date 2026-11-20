package state

import (
	"context"
	"testing"
	"time"

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

	// Append more than the cap (P2-9: ChatHistoryMaxMessages = 30)
	for i := 0; i < ChatHistoryMaxMessages+5; i++ {
		msg := models.ChatMessage{Role: "user", Content: "Message"}
		err = store.AppendChatHistory(ctx, userID, msg)
		require.NoError(t, err)
	}

	history, err = store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, ChatHistoryMaxMessages)
}

// P2-9: chat history lives 24h in Redis (durable copy: Postgres audit_sessions).
func TestChatHistory_TTLIs24h(t *testing.T) {
	store, mr, cleanup := setupTestStore(t)
	defer cleanup()
	require.NoError(t, store.AppendChatHistory(context.Background(), "u-ttl", models.ChatMessage{Role: "user", Content: "hi"}))
	assert.Equal(t, 24*time.Hour, mr.TTL("chat:u-ttl"))
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

func TestPairingRateLimit(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "unauthorized-user"

	// Initially not locked out
	locked, dur, err := store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.False(t, locked)
	assert.Zero(t, dur)

	// Record 4 failed attempts - not locked out
	for i := 1; i <= 4; i++ {
		attempts, isLocked, err := store.RecordFailedPairingAttempt(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, int64(i), attempts)
		assert.False(t, isLocked)
	}

	// 5th attempt triggers lockout
	attempts, isLocked, err := store.RecordFailedPairingAttempt(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), attempts)
	assert.True(t, isLocked)

	// Verify lockout check returns true
	locked, dur, err = store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.True(t, dur > 0)

	// Further attempt still reports locked
	_, isLocked, err = store.RecordFailedPairingAttempt(ctx, userID)
	require.NoError(t, err)
	assert.True(t, isLocked)

	// Reset clears lockout and attempts
	err = store.ResetPairingRateLimit(ctx, userID)
	require.NoError(t, err)

	locked, _, err = store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.False(t, locked)
}

func TestOAuthToken_StoredEncrypted(t *testing.T) {
	store, mr, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-encrypt-check"
	plainToken := []byte(`{"access_token":"ya29.secret","refresh_token":"1//ref"}`)

	err := store.SetOAuthToken(ctx, userID, plainToken)
	require.NoError(t, err)

	// Inspect raw string in Redis
	rawRedisVal, err := mr.Get("token:" + userID)
	require.NoError(t, err)
	assert.NotEqual(t, string(plainToken), rawRedisVal, "Token in Redis must NOT be plaintext")

	// GetOAuthToken decrypts and returns original plaintext
	retrieved, err := store.GetOAuthToken(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, plainToken, retrieved)
}

func TestOAuthState_SaveAndValidate(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	nonce := "abc123xyz789nonce"
	userID := "user-state-1"

	err := store.SaveOAuthState(ctx, nonce, userID, 10*time.Minute)
	require.NoError(t, err)

	// First validation must succeed and return userID
	val, err := store.ValidateAndDeleteOAuthState(ctx, nonce)
	require.NoError(t, err)
	assert.Equal(t, userID, val)

	// Second validation must fail (nonce atomically deleted)
	_, err = store.ValidateAndDeleteOAuthState(ctx, nonce)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid or expired")
}

func TestCredentialFallback_Invocation(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-fallback-test"
	fallbackToken := []byte(`{"access_token":"fallback_token_data"}`)

	// Configure fallback getter
	fallbackCalls := 0
	store.SetCredentialFallback(func(ctx context.Context, uID string) ([]byte, error) {
		fallbackCalls++
		assert.Equal(t, userID, uID)
		return fallbackToken, nil
	})

	// First call hits fallback and populates Redis
	token, err := store.GetOAuthToken(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, fallbackToken, token)
	assert.Equal(t, 1, fallbackCalls)

	// Second call hits Redis cache (encrypted) without calling fallback
	token, err = store.GetOAuthToken(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, fallbackToken, token)
	assert.Equal(t, 1, fallbackCalls)
}

func TestStagePendingMetaTool_RoundTrip(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-meta-test"
	action := PendingMetaToolAction{
		ToolName:  "update_user_preferences",
		Args:      map[string]interface{}{"standing_instructions": "be brief"},
		UserID:    userID,
		CreatedAt: time.Now(),
	}

	err := store.StagePendingMetaTool(ctx, userID, action, 5*time.Minute)
	require.NoError(t, err)

	// Retrieve and clear
	retrieved, err := store.GetAndClearPendingMetaTool(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, action.ToolName, retrieved.ToolName)
	assert.Equal(t, action.UserID, retrieved.UserID)

	// Second retrieval should be nil
	retrieved2, err := store.GetAndClearPendingMetaTool(ctx, userID)
	require.NoError(t, err)
	assert.Nil(t, retrieved2)
}

func TestAppendChatHistory_StripsPhotoData(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "test-user-photo"
	largePhoto := make([]byte, 1024*1024*2) // 2 MB

	msg := models.ChatMessage{
		Role:          "user",
		Content:       "Check this receipt",
		PhotoData:     largePhoto,
		PhotoMimeType: "image/jpeg",
	}

	err := store.AppendChatHistory(ctx, userID, msg)
	require.NoError(t, err)

	history, err := store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 1)

	assert.Nil(t, history[0].PhotoData)
	assert.Empty(t, history[0].PhotoMimeType)
	assert.Contains(t, history[0].Content, "[Attachment: image/jpeg (2.0 MB)]")
	assert.Contains(t, history[0].Content, "Check this receipt")
}

func TestGetChatHistory_ClearsLegacyPhotoData(t *testing.T) {
	store, mr, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	userID := "test-user-legacy"

	// Pre-seed raw JSON with PhotoData directly into Redis
	legacyJSON := `{"role":"user","content":"Old photo","photo_data":"AQIDBA==","photo_mime_type":"image/png"}`
	_, _ = mr.RPush("chat:"+userID, legacyJSON)

	history, err := store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "Old photo", history[0].Content)
	assert.Nil(t, history[0].PhotoData)
	assert.Empty(t, history[0].PhotoMimeType)
}

func TestPromptCache_SetAndGet(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	familyGroup := "fam-101"
	userID := "user-1"
	module := "baby"
	content := "Baby Liam is 8 months old. Last feeding at 08:30."

	// Initial get: cache miss
	cached, found, err := store.GetPromptSection(ctx, familyGroup, userID, module)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, cached)

	// Set prompt section with 10m TTL
	err = store.SetPromptSection(ctx, familyGroup, userID, module, content, 10*time.Minute)
	require.NoError(t, err)

	// Subsequent get: cache hit for the same user only
	cached, found, err = store.GetPromptSection(ctx, familyGroup, userID, module)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, content, cached)

	_, found, err = store.GetPromptSection(ctx, familyGroup, "user-2", module)
	require.NoError(t, err)
	assert.False(t, found, "another family member must not hit this user's cached section")

	// Empty familyGroup, userID or module error validation
	assert.Error(t, store.SetPromptSection(ctx, "", userID, module, content, 0))
	assert.Error(t, store.SetPromptSection(ctx, familyGroup, "", module, content, 0))
	assert.Error(t, store.SetPromptSection(ctx, familyGroup, userID, "", content, 0))
}

func TestPromptCache_GetSections_MGet(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	familyGroup := "fam-202"
	userID := "user-1"

	// Pre-populate some sections
	require.NoError(t, store.SetPromptSection(ctx, familyGroup, userID, "baby", "Baby section", 10*time.Minute))
	require.NoError(t, store.SetPromptSection(ctx, familyGroup, userID, "finance", "Finance section", 10*time.Minute))

	// Query 4 modules: 2 hits, 2 misses
	modules := []string{"baby", "house", "finance", "schedule"}
	hits, misses, err := store.GetPromptSections(ctx, familyGroup, userID, modules)
	require.NoError(t, err)

	assert.Len(t, hits, 2)
	assert.Equal(t, "Baby section", hits["baby"])
	assert.Equal(t, "Finance section", hits["finance"])

	assert.ElementsMatch(t, []string{"house", "schedule"}, misses)
}

func TestPromptCache_InvalidateSections(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	familyGroup := "fam-303"
	userID := "user-1"

	require.NoError(t, store.SetPromptSection(ctx, familyGroup, userID, "baby", "Baby section", 10*time.Minute))
	require.NoError(t, store.SetPromptSection(ctx, familyGroup, userID, "house", "House section", 10*time.Minute))

	// Invalidate both by explicit key
	err := store.InvalidatePromptSections(ctx,
		PromptCacheKey(familyGroup, userID, "baby"),
		PromptCacheKey(familyGroup, userID, "house"))
	require.NoError(t, err)

	// Verify both evicted
	_, found1, _ := store.GetPromptSection(ctx, familyGroup, userID, "baby")
	_, found2, _ := store.GetPromptSection(ctx, familyGroup, userID, "house")
	assert.False(t, found1)
	assert.False(t, found2)
}

func TestPromptCache_InvalidateFamily_AllUsersOneModule(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, u := range []string{"u1", "u2"} {
		require.NoError(t, store.SetPromptSection(ctx, "fam-404", u, "memory", "m", time.Minute))
		require.NoError(t, store.SetPromptSection(ctx, "fam-404", u, "baby", "b", time.Minute))
	}
	n, err := store.InvalidateFamilyPromptSections(ctx, "fam-404", []string{"memory"})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	for _, u := range []string{"u1", "u2"} {
		_, found, _ := store.GetPromptSection(ctx, "fam-404", u, "memory")
		assert.False(t, found)
		_, found, _ = store.GetPromptSection(ctx, "fam-404", u, "baby")
		assert.True(t, found)
	}
}

// ADR 0002 Decision 4: a limit on wrong codes across all senders. Until the limit, codes are
// accepted; at the limit, every code is refused until the window ends.
func TestCodesBlocked_AfterTheInstallWideLimit(t *testing.T) {
	store, mr, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	assert.False(t, store.CodesBlocked(ctx))
	for i := 0; i < GlobalCodeFailureLimit-1; i++ {
		require.NoError(t, store.RecordFailedCodeAttempt(ctx))
	}
	assert.False(t, store.CodesBlocked(ctx))
	require.NoError(t, store.RecordFailedCodeAttempt(ctx))
	assert.True(t, store.CodesBlocked(ctx))
	mr.FastForward(GlobalCodeWindow + time.Second)
	assert.False(t, store.CodesBlocked(ctx), "the window ends")
}

// V4-32: a person's Google sign-in is found through all their accounts, preferred first, until
// V4-23 keys sign-ins by person.
func TestGetOAuthToken_FoundThroughThePersonsAccounts(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	require.NoError(t, store.SetOAuthToken(ctx, "+15550001001", []byte(`{"access_token":"wa"}`)))

	tok, err := store.GetOAuthToken(ctx, "7001")
	require.NoError(t, err)
	assert.Nil(t, tok, "without account keys, only the exact key is read")

	store.SetAccountKeys(func(_ context.Context, key string) ([]string, error) {
		if key == "7001" {
			return []string{"7001", "+15550001001"}, nil
		}
		return nil, nil
	})
	tok, err = store.GetOAuthToken(ctx, "7001")
	require.NoError(t, err)
	assert.JSONEq(t, `{"access_token":"wa"}`, string(tok), "found under the person's other account")

	tok, err = store.GetOAuthToken(ctx, "7999")
	require.NoError(t, err)
	assert.Nil(t, tok, "another person's sign-in is never found")
}
