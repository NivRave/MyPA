package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAdversarial_SEC03_TelegramWebhookSecretVerification(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			SecretToken: "correct-secret-token-abcdef123456",
		},
	}
	r := setupIngressRouter(pub, cfg)

	validPayload := []byte(`{
		"update_id": 9999,
		"message": {
			"message_id": 1,
			"from": {"id": 111222, "is_bot": false, "first_name": "Adv"},
			"chat": {"id": 111222, "type": "private"},
			"date": 1690000000,
			"text": "test attack"
		}
	}`)

	forgedTokens := []struct {
		name         string
		headerVal    string
		hasHeader    bool
		expectedCode int
	}{
		{"MissingHeader", "", false, http.StatusUnauthorized},
		{"EmptyHeader", "", true, http.StatusUnauthorized},
		{"WrongToken", "totally-wrong-token", true, http.StatusUnauthorized},
		{"SingleCharDiff", "correct-secret-token-abcdef123457", true, http.StatusUnauthorized},
		{"PrefixOnly", "correct-secret-token-", true, http.StatusUnauthorized},
		{"ExtraSuffix", "correct-secret-token-abcdef123456-extra", true, http.StatusUnauthorized},
	}

	for _, tc := range forgedTokens {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(validPayload))
			req.Header.Set("Content-Type", "application/json")
			if tc.hasHeader {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.headerVal)
			}
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			assert.Equal(t, tc.expectedCode, rr.Code)
			assert.Contains(t, rr.Body.String(), "Unauthorized")
		})
	}

	// Valid token should succeed
	t.Run("ValidSecretTokenPasses", func(t *testing.T) {
		pub.On("Publish", mock.Anything, mock.Anything).Return(nil).Once()

		req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(validPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "correct-secret-token-abcdef123456")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		pub.AssertExpectations(t)
	})
}

func TestAdversarial_SEC03_TwilioWebhookHMACVerification(t *testing.T) {
	pub := new(MockEventPublisher)
	authToken := "twilio-secret-auth-token-xyz"
	cfg := &config.Config{
		Twilio: config.TwilioConfig{
			AuthToken: authToken,
		},
	}
	r := setupIngressRouter(pub, cfg)

	data := url.Values{}
	data.Set("Body", "Adversarial Twilio Test")
	data.Set("From", "whatsapp:+15550001")
	data.Set("MessageSid", "SM123456789")

	// 1. Missing signature
	t.Run("MissingTwilioSignature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/webhook/twilio", strings.NewReader(data.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "Forbidden: invalid signature")
	})

	// 2. Forged signature
	t.Run("ForgedTwilioSignature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/webhook/twilio", strings.NewReader(data.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", "forged-base64-signature==")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "Forbidden: invalid signature")
	})

	// 3. Signature for different URL/Payload
	t.Run("MismatchedPayloadSignature", func(t *testing.T) {
		// Calculate signature for "Legit Body"
		legitData := url.Values{}
		legitData.Set("Body", "Legit Body")
		legitData.Set("From", "whatsapp:+15550001")
		legitData.Set("MessageSid", "SM123456789")

		sigPayload := "http://example.com/webhook/twilio" + "BodyLegit Body" + "Fromwhatsapp:+15550001" + "MessageSidSM123456789"
		mac := hmac.New(sha1.New, []byte(authToken))
		mac.Write([]byte(sigPayload))
		legitSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

		// Send with tampered body "Adversarial Twilio Test" but signature for "Legit Body"
		req := httptest.NewRequest("POST", "http://example.com/webhook/twilio", strings.NewReader(data.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", legitSig)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	// 4. Valid signature passes
	t.Run("ValidSignaturePasses", func(t *testing.T) {
		pub.On("Publish", mock.Anything, mock.Anything).Return(nil).Once()

		fullURL := "https://gateway.mypa.internal/webhook/twilio"
		sigPayload := fullURL + "BodyAdversarial Twilio Test" + "Fromwhatsapp:+15550001" + "MessageSidSM123456789"
		mac := hmac.New(sha1.New, []byte(authToken))
		mac.Write([]byte(sigPayload))
		validSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest("POST", fullURL, strings.NewReader(data.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "gateway.mypa.internal")
		req.Header.Set("X-Twilio-Signature", validSig)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		pub.AssertExpectations(t)
	})
}
