package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_GetWebhookInfo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/getWebhookInfo", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"https://x.example/webhook/telegram","has_custom_certificate":false,` +
			`"pending_update_count":3,"last_error_date":1763542800,"last_error_message":"Connection refused","max_connections":40}}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	info, err := client.GetWebhookInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://x.example/webhook/telegram", info.URL)
	assert.Equal(t, 3, info.PendingUpdateCount)
	assert.Equal(t, "Connection refused", info.LastErrorMessage)
	assert.True(t, info.LastErrorAt().Equal(time.Unix(1763542800, 0)))
}

func TestClient_GetWebhookInfo_NothingRegistered(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"","has_custom_certificate":false,"pending_update_count":0}}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	info, err := client.GetWebhookInfo(context.Background())
	require.NoError(t, err)
	assert.Empty(t, info.URL)
	assert.True(t, info.LastErrorAt().IsZero(), "no error on record must be the zero time, not 1970")
}

func TestClient_GetWebhookInfo_RejectedTokenIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	_, err := client.GetWebhookInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unauthorized")
	assert.NotContains(t, err.Error(), "test-token", "the bot token must never appear in an error")
}

func TestClient_GetWebhookInfo_TransportErrorHidesTheToken(t *testing.T) {
	client := NewClient("secret-bot-token")
	client.httpClient.Transport = &rewriteTransport{URL: "http://127.0.0.1:1"} // nothing listens here

	_, err := client.GetWebhookInfo(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-bot-token")
}
