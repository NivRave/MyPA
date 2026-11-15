package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_SetWebhook_SendsURLAndSecret(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/setWebhook", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"ok":true,"result":true,"description":"Webhook was set"}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SetWebhook(context.Background(), "https://x.example/webhook/telegram", "s3cret_token-1")
	require.NoError(t, err)
	assert.Equal(t, "https://x.example/webhook/telegram", got["url"])
	assert.Equal(t, "s3cret_token-1", got["secret_token"])
}

func TestClient_SetWebhook_APIErrorReturned(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: bad webhook: HTTPS url must be provided for webhook"}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SetWebhook(context.Background(), "http://insecure.example", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTPS url must be provided")
}

func TestClient_SetWebhook_OkFalseWith200IsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"nope"}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SetWebhook(context.Background(), "https://x.example", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// Transport errors from net/http embed the full request URL, which contains the bot token.
// SetWebhook's error is logged at startup, so it must not leak the token.
func TestClient_SetWebhook_TransportErrorDoesNotLeakBotToken(t *testing.T) {
	client := NewClient("123456:SUPER-SECRET-BOT-TOKEN")
	client.httpClient.Transport = failingTransport{}

	err := client.SetWebhook(context.Background(), "https://x.example", "s3cret")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SUPER-SECRET-BOT-TOKEN")
	assert.Contains(t, err.Error(), "connection refused")
}
