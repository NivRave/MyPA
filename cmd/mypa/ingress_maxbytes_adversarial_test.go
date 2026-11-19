package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_MaxBytesReader_TelegramWebhook verifies that sending a 12MB payload
// (exceeding the 10MB gateway webhook limit) triggers an HTTP error and does NOT crash the server.
func TestAdversarial_MaxBytesReader_TelegramWebhook(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{Channels: adapters.Config{Telegram: telegram.Config{SecretToken: testTelegramSecret}}}
	r := setupIngressRouter(pub, testChannels(t, cfg))

	// Create an oversized stream: 12MB of data
	oversizedStream := newRepeatingReader(`{"update_id":1,"message":{"text":"A"}}`, 12<<20)

	req := httptest.NewRequest("POST", "/webhook/telegram", oversizedStream)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	rr := httptest.NewRecorder()

	// Ensure no panic occurs
	require.NotPanics(t, func() {
		r.ServeHTTP(rr, req)
	})

	// MaxBytesReader causes io.ReadAll to return an error, returning HTTP 400 Bad Request
	assert.Equal(t, http.StatusBadRequest, rr.Code, "Oversized Telegram payload must return HTTP 400")
	assert.Contains(t, rr.Body.String(), "bad request")

	// Follow-up: verify the server remains healthy and can process subsequent legitimate requests
	validPayload := []byte(`{"update_id": 999}`)
	validReq := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(validPayload))
	validReq.Header.Set("Content-Type", "application/json")
	validReq.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	validRR := httptest.NewRecorder()

	require.NotPanics(t, func() {
		r.ServeHTTP(validRR, validReq)
	})
	assert.Equal(t, http.StatusOK, validRR.Code, "Server must remain functional after handling oversized payload")
}

// TestAdversarial_MaxBytesReader_TwilioWebhook verifies that an oversized form body
// (> 10MB) sent to /webhook/twilio is rejected by ParseForm without crashing the server.
func TestAdversarial_MaxBytesReader_TwilioWebhook(t *testing.T) {
	pub := new(MockEventPublisher)
	const twilioToken = "maxbytes-twilio-token"
	cfg := &config.Config{Channels: adapters.Config{WhatsApp: twilio.Config{AuthToken: twilioToken}}}
	r := setupIngressRouter(pub, testChannels(t, cfg))

	// Generate 11MB form body: "From=%2B1234567890&Body=" followed by 11MB of filler
	formPrefix := "From=%2B1234567890&Body="
	oversizedStream := newRepeatingReader(formPrefix+strings.Repeat("X", 1024), 11<<20)

	req := httptest.NewRequest("POST", "/webhook/twilio", oversizedStream)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	require.NotPanics(t, func() {
		r.ServeHTTP(rr, req)
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code, "Oversized Twilio payload must return HTTP 400")
	assert.Contains(t, rr.Body.String(), "Bad request")

	// Verify server remains healthy
	normalForm := url.Values{
		"From": {"+1234567890"},
		"Body": {"Valid message"},
	}
	validReq := httptest.NewRequest("POST", "http://example.com/webhook/twilio", strings.NewReader(normalForm.Encode()))
	validReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	validReq.Header.Set("X-Twilio-Signature", signTwilio(twilioToken, "http://example.com/webhook/twilio", normalForm))
	validRR := httptest.NewRecorder()

	pub.On("Publish", mock.Anything, mock.Anything).Return(nil)

	require.NotPanics(t, func() {
		r.ServeHTTP(validRR, validReq)
	})
	assert.Equal(t, http.StatusOK, validRR.Code, "Server must remain functional after handling oversized payload")
}
