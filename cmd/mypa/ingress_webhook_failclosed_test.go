package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	r := setupIngressRouter(pub, testChannels(t, &config.Config{})) // no SecretToken configured

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
	r := setupIngressRouter(pub, testChannels(t, &config.Config{})) // no Twilio AuthToken configured

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

// BL-8: the delivery-status route exists (not 404/405) and is signature-protected like the inbound
// one: a request that fails verification publishes nothing. V4-33: a verified failure report is
// published once, as a delivery report and not as a message, for the delivery policy.
func TestTwilioStatusCallback_RouteIsWiredAndFailsClosed(t *testing.T) {
	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"failed"}, "ErrorCode": {"63015"}}
	fullURL := "https://gw.example/webhook/twilio/status"

	for name, cfg := range map[string]*config.Config{
		"no token":        {},
		"wrong signature": {Channels: adapters.Config{WhatsApp: twilio.Config{AuthToken: "real-token"}}},
	} {
		t.Run(name, func(t *testing.T) {
			pub := new(MockEventPublisher)
			req := httptest.NewRequest("POST", fullURL, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-Host", "gw.example")
			req.Header.Set("X-Twilio-Signature", signTwilio("", fullURL, form))
			rr := httptest.NewRecorder()
			setupIngressRouter(pub, testChannels(t, cfg)).ServeHTTP(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
			pub.AssertNotCalled(t, "Publish", mock.Anything, mock.Anything)
		})
	}

	t.Run("valid signature", func(t *testing.T) {
		pub := new(MockEventPublisher)
		pub.On("Publish", mock.Anything, mock.MatchedBy(func(v any) bool {
			in, ok := v.(channel.Inbound)
			return ok && in.Report != nil && in.Report.Ref == "SM1" && in.Report.Reason == channel.NotReachable && in.Text == ""
		})).Return(nil).Once()
		req := httptest.NewRequest("POST", fullURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "gw.example")
		req.Header.Set("X-Twilio-Signature", signTwilio("real-token", fullURL, form))
		rr := httptest.NewRecorder()
		setupIngressRouter(pub, testChannels(t, &config.Config{Channels: adapters.Config{WhatsApp: twilio.Config{AuthToken: "real-token"}}})).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		pub.AssertExpectations(t)
	})
}
