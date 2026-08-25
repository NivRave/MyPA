package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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

	// Setup router
	mux := setupAuthRouter(oauthCfg, store, tgClient)

	// Run test
	req := httptest.NewRequest("GET", "/auth/google/callback?code=my-test-code&state=user-123", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	// Verify
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "Success!")
	
	// Verify token was saved
	tokenBytes, err := store.GetOAuthToken(context.Background(), "user-123")
	require.NoError(t, err)
	assert.Contains(t, string(tokenBytes), "dummy-token")

	tgClient.AssertExpectations(t)
}

func TestAuthRouter_MissingParams(t *testing.T) {
	mux := setupAuthRouter(nil, nil, nil) // Dependencies aren't used when params are missing

	req := httptest.NewRequest("GET", "/auth/google/callback", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
