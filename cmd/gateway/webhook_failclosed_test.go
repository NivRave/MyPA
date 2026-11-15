package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testTelegramSecret = "test-telegram-secret_ABC-123"

// signTwilio computes a Twilio X-Twilio-Signature for fullURL + sorted form params.
func signTwilio(authToken, fullURL string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(fullURL)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(form.Get(k))
	}
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(sb.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// --- Telegram: handler must fail closed when no secret is configured ---

func TestTelegramWebhook_EmptyConfiguredSecret_RejectsEverything(t *testing.T) {
	pub := new(MockEventPublisher)
	r := setupRouter(pub, &config.Config{}) // no SecretToken configured

	payload := []byte(`{"update_id":1,"message":{"message_id":1,"from":{"id":42},"chat":{"id":99},"text":"hi"}}`)

	cases := []struct {
		name      string
		hasHeader bool
		value     string
	}{
		{"MissingHeader", false, ""},
		{"EmptyHeader", true, ""},
		{"ArbitraryHeader", true, "anything"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewReader(payload))
			if tc.hasHeader {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.value)
			}
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})
	}
	pub.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything)
}

// --- Twilio: handler must fail closed when no auth token is configured ---

func TestTwilioWebhook_EmptyAuthToken_RejectsEvenEmptyKeySignature(t *testing.T) {
	pub := new(MockEventPublisher)
	r := setupRouter(pub, &config.Config{}) // no Twilio AuthToken configured

	form := url.Values{"From": {"whatsapp:+15550001"}, "Body": {"forged"}, "MessageSid": {"SM1"}}
	fullURL := "https://gw.example/webhook/twilio"

	req := httptest.NewRequest("POST", fullURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "gw.example")
	// An attacker can compute an HMAC with an empty key — it must not be accepted.
	req.Header.Set("X-Twilio-Signature", signTwilio("", fullURL, form))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	pub.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything)
}

// --- setWebhook registration with retry/backoff ---

type fakeRegistrar struct {
	mu        sync.Mutex
	failFirst int
	calls     int
	gotURL    string
	gotSecret string
}

func (f *fakeRegistrar) SetWebhook(_ context.Context, webhookURL, secretToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotURL, f.gotSecret = webhookURL, secretToken
	if f.calls <= f.failFirst {
		return errors.New("network down")
	}
	return nil
}

func TestRegisterWebhookWithRetry_SucceedsAfterFailures(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 2}
	err := registerWebhookWithRetry(context.Background(), reg, "https://x.example/webhook/telegram", testTelegramSecret, 5, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, 3, reg.calls)
	assert.Equal(t, "https://x.example/webhook/telegram", reg.gotURL)
	assert.Equal(t, testTelegramSecret, reg.gotSecret)
}

func TestRegisterWebhookWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 100}
	err := registerWebhookWithRetry(context.Background(), reg, "https://x.example/webhook/telegram", testTelegramSecret, 3, time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, 3, reg.calls)
}

func TestRegisterWebhookWithRetry_StopsOnContextCancel(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := registerWebhookWithRetry(ctx, reg, "https://x.example/webhook/telegram", testTelegramSecret, 10, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	assert.LessOrEqual(t, reg.calls, 1)
}
