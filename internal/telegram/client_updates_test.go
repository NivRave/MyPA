package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-40: removing the webhook to poll keeps the updates Telegram is holding.
func TestClient_DeleteWebhook_KeepsPendingUpdates(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/deleteWebhook", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"ok":true,"result":true,"description":"Webhook was deleted"}`))
	}))
	defer ts.Close()
	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	require.NoError(t, client.DeleteWebhook(context.Background()))
	assert.Equal(t, false, got["drop_pending_updates"])
}

func TestClient_GetUpdates_SendsOffsetAndTimeoutAndReturnsEachUpdate(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/getUpdates", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"from":{"id":5},"chat":{"id":5},"text":"hi"}},{"update_id":8}]}`))
	}))
	defer ts.Close()
	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	updates, err := client.GetUpdates(context.Background(), 7, 25*time.Second)
	require.NoError(t, err)
	assert.Equal(t, float64(7), got["offset"])
	assert.Equal(t, float64(25), got["timeout"], "seconds, as the Bot API takes it")
	require.Len(t, updates, 2)
	msg, err := ParseUpdate(updates[0])
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "hi", msg.Text, "each update parses exactly as a webhook body")
}

func TestClient_GetUpdates_ConflictIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
	}))
	defer ts.Close()
	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	_, err := client.GetUpdates(context.Background(), 0, time.Second)
	assert.ErrorIs(t, err, ErrConflict)
}

// P1-1: the bot token is in the request URL; no error may carry it.
func TestClient_UpdateCallsNeverLeakTheToken(t *testing.T) {
	const token = "123456:SECRET-TOKEN-VALUE"
	client := NewClient(token)
	client.httpClient.Transport = &rewriteTransport{URL: "http://127.0.0.1:1"} // nothing listens
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.DeleteWebhook(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN-VALUE")
	_, err = client.GetUpdates(ctx, 0, time.Second)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN-VALUE")
}
