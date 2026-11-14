package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// TestSEC07_OAuth_AdversarialSuite stress tests:
// 1. Raw Redis token inspection (must be AES-256-GCM encrypted, never plaintext).
// 2. Replay attacks on CSRF nonce (must be strictly single-use).
// 3. Expired CSRF nonces (must return HTTP 403).
// 4. Invalid CSRF nonces (must return HTTP 403).
// 5. PostgreSQL fallback recovery on Redis cache miss and automatic re-population.
func TestSEC07_OAuth_AdversarialSuite(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	mockOAuthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "super-secret-access-token-xyz",
			"token_type":    "Bearer",
			"refresh_token": "refresh-token-xyz",
			"expiry":        time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	defer mockOAuthServer.Close()

	cfg := &config.GoogleConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL:  "http://localhost:8081/auth/google/callback",
	}
	oauthCfg := calendar.NewOAuthConfig(cfg)
	oauthCfg.SetEndpoint(oauth2.Endpoint{
		TokenURL: mockOAuthServer.URL,
	})

	mockDBFallback := make(map[string][]byte)
	saver := &mockFallbackSaver{
		saveFn: func(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error {
			mockDBFallback[userID+":"+provider] = tokenData
			return nil
		},
	}

	// Configure fallback getter on store
	store.SetCredentialFallback(func(ctx context.Context, userID string) ([]byte, error) {
		data, ok := mockDBFallback[userID+":google_oauth"]
		if !ok {
			return nil, fmt.Errorf("no credential in fallback DB")
		}
		return data, nil
	})

	tgClient := new(MockTelegramClient)
	tgClient.On("SendMessage", mock.Anything, "victim-user", mock.Anything).Return(nil).Maybe()

	mux := setupAuthRouter(oauthCfg, store, tgClient, saver)

	// 1. Replay Attack & Single-Use Nonce Test
	t.Run("ReplayAttack_NonceIsSingleUse", func(t *testing.T) {
		nonce := "replay-test-nonce-1234567890123456"
		err := store.SaveOAuthState(context.Background(), nonce, "victim-user", 5*time.Minute)
		require.NoError(t, err)

		// First request: Should succeed
		req1 := httptest.NewRequest("GET", "/auth/google/callback?code=first-code&state="+nonce, nil)
		rr1 := httptest.NewRecorder()
		mux.ServeHTTP(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code, "First callback with valid nonce must succeed")

		// Replay request: Same nonce must be rejected with HTTP 403 Forbidden
		req2 := httptest.NewRequest("GET", "/auth/google/callback?code=replay-code&state="+nonce, nil)
		rr2 := httptest.NewRecorder()
		mux.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusForbidden, rr2.Code, "Replayed callback must be rejected with 403")
		assert.Contains(t, rr2.Body.String(), "invalid or expired oauth state nonce")
	})

	// 2. Expired Nonce Test
	t.Run("ExpiredNonce_Rejected", func(t *testing.T) {
		expiredNonce := "expired-nonce-99999"
		err := store.SaveOAuthState(context.Background(), expiredNonce, "victim-user", 50*time.Millisecond)
		require.NoError(t, err)

		// Fast-forward miniredis clock
		mr.FastForward(100 * time.Millisecond)

		req := httptest.NewRequest("GET", "/auth/google/callback?code=any-code&state="+expiredNonce, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid or expired oauth state nonce")
	})

	// 3. Raw Redis Token Inspection (Must be AES-256-GCM encrypted)
	t.Run("RawRedisToken_IsEncryptedAESGCM", func(t *testing.T) {
		rawToken, err := mr.Get("token:victim-user")
		require.NoError(t, err)
		require.NotEmpty(t, rawToken)

		// Assert raw value is NOT plaintext JSON
		assert.NotContains(t, rawToken, "super-secret-access-token-xyz", "Redis must NOT store plaintext access token")
		assert.NotContains(t, rawToken, "refresh-token-xyz", "Redis must NOT store plaintext refresh token")

		// Assert raw value is valid AES-GCM ciphertext layout (>= 44 bytes)
		rawBytes := []byte(rawToken)
		assert.GreaterOrEqual(t, len(rawBytes), crypto.MinCiphertextSize, "Redis token must be >= 44 bytes for [16 salt][12 nonce][cipher+16 tag]")

		// Decrypt via crypto.Decrypt to verify authenticity
		decrypted, decErr := crypto.Decrypt(rawBytes, "")
		require.NoError(t, decErr, "Redis token must be decryptable with system encryption key")
		assert.Contains(t, string(decrypted), "super-secret-access-token-xyz")
	})

	// 4. PostgreSQL Fallback Recovery on Redis Cache Miss
	t.Run("FallbackRecovery_OnCacheMiss", func(t *testing.T) {
		// Verify fallback exists in mockDBFallback
		assert.NotEmpty(t, mockDBFallback["victim-user:google_oauth"])

		// Wipe Redis completely (simulating Redis crash/restart/flush)
		mr.FlushAll()

		// Verify token is gone from Redis
		_, err := mr.Get("token:victim-user")
		assert.Error(t, err, "Token must be absent from Redis after FlushAll")

		// Call store.GetOAuthToken — must trigger fallback getter and recover token
		recovered, err := store.GetOAuthToken(context.Background(), "victim-user")
		require.NoError(t, err)
		require.NotEmpty(t, recovered)
		assert.Contains(t, string(recovered), "super-secret-access-token-xyz")

		// Verify Redis was automatically re-populated with encrypted token
		rePopulatedRaw, err := mr.Get("token:victim-user")
		require.NoError(t, err)
		assert.NotContains(t, rePopulatedRaw, "super-secret-access-token-xyz", "Re-populated token must also be encrypted")
	})
}
