package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type MockTelegramClient struct {
	mock.Mock
}

func (m *MockTelegramClient) SendMessage(ctx context.Context, chatID string, text string) error {
	args := m.Called(ctx, chatID, text)
	return args.Error(0)
}

func TestAuthRouter(t *testing.T) {
	// Setup Redis
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	// Setup Mock OAuth server
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		_ = r.ParseForm()
		assert.Equal(t, "my-test-code", r.Form.Get("code"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "dummy-token",
			"token_type": "Bearer",
			"expires_in": 3600,
			"refresh_token": "dummy-refresh"
		}`))
	}))
	defer oauthSrv.Close()

	// Setup OAuth config pointing to our mock server
	cfg := &config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	}
	oauthCfg := calendar.NewOAuthConfig(cfg)
	oauthCfg.SetEndpoint(oauth2.Endpoint{
		TokenURL: oauthSrv.URL, // Inject mock token URL
	})

	// Setup mock Telegram
	tgClient := new(MockTelegramClient)
	tgClient.On("SendMessage", mock.Anything, "user-123", mock.MatchedBy(func(s string) bool {
		return len(s) > 0
	})).Return(nil)

	// Pre-save CSRF nonce in Redis
	csrfNonce := "valid-csrf-nonce-123"
	err = store.SaveOAuthState(context.Background(), csrfNonce, "user-123", 10*time.Minute)
	require.NoError(t, err)

	// Setup mock fallback saver
	var savedUserID, savedProvider string
	var savedTokenData []byte
	mockSaver := &mockFallbackSaver{
		saveFn: func(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error {
			savedUserID = userID
			savedProvider = provider
			savedTokenData = tokenData
			return nil
		},
	}

	// Setup router with fallback saver
	mux := setupAuthRouter(oauthCfg, store, tgClient, mockSaver)

	// Run test with valid nonce
	req := httptest.NewRequest("GET", "/auth/google/callback?code=my-test-code&state="+csrfNonce, nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	// Verify
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "Success!")

	// Verify token was saved in Redis
	tokenBytes, err := store.GetOAuthToken(context.Background(), "user-123")
	require.NoError(t, err)
	assert.Contains(t, string(tokenBytes), "dummy-token")

	// Verify fallback was persisted
	assert.Equal(t, "user-123", savedUserID)
	assert.Equal(t, "google_oauth", savedProvider)
	assert.Contains(t, string(savedTokenData), "dummy-token")

	// Verify state nonce was deleted (one-time use)
	_, err = store.ValidateAndDeleteOAuthState(context.Background(), csrfNonce)
	assert.Error(t, err)

	tgClient.AssertExpectations(t)
}

type mockFallbackSaver struct {
	saveFn func(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error
}

func (m *mockFallbackSaver) SaveCredentialFallback(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, userID, provider, tokenData, passphrase...)
	}
	return nil
}

func TestAuthRouter_InvalidStateNonce(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	mux := setupAuthRouter(nil, store, nil)
	req := httptest.NewRequest("GET", "/auth/google/callback?code=any-code&state=non-existent-nonce", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid or expired oauth state nonce")
}

func TestAuthRouter_MissingParams(t *testing.T) {
	mux := setupAuthRouter(nil, nil, nil) // Dependencies aren't used when params are missing

	req := httptest.NewRequest("GET", "/auth/google/callback", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
