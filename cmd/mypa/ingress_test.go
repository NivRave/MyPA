package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockEventPublisher is a mock of the EventPublisher interface.
type MockEventPublisher struct {
	mock.Mock
}

func (m *MockEventPublisher) Publish(ctx context.Context, msg any) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func TestGatewayHealth(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{}
	r := setupIngressRouter(pub, cfg)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestGatewayTelegramWebhook_Success(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{Telegram: config.TelegramConfig{SecretToken: testTelegramSecret}}
	r := setupIngressRouter(pub, cfg)

	// Valid Telegram Update payload
	payload := []byte(`{
		"update_id": 12345,
		"message": {
			"message_id": 1,
			"from": {
				"id": 987654321,
				"is_bot": false,
				"first_name": "Test",
				"username": "testuser",
				"language_code": "en"
			},
			"chat": {
				"id": 987654321,
				"first_name": "Test",
				"username": "testuser",
				"type": "private"
			},
			"date": 1690000000,
			"text": "Hello bot"
		}
	}`)

	// We expect the message to be published
	pub.On("Publish", mock.Anything, mock.MatchedBy(func(msg models.Message) bool {
		return msg.UserID == "987654321" && msg.Text == "Hello bot"
	})).Return(nil)

	req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	pub.AssertExpectations(t)
}

func TestGatewayTelegramWebhook_InvalidJSON(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{Telegram: config.TelegramConfig{SecretToken: testTelegramSecret}}
	r := setupIngressRouter(pub, cfg)

	payload := []byte(`{"update_id": 12345, bad json`)

	req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// In the real code we return 200 on parse error to avoid telegram retries
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGatewayTelegramWebhook_SecretToken(t *testing.T) {
	pub := new(MockEventPublisher)
	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			SecretToken: "super-secret-token-123",
		},
	}
	r := setupIngressRouter(pub, cfg)

	payload := []byte(`{"update_id": 12345}`)

	// 1. Missing header -> 401
	req1 := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, req1)
	assert.Equal(t, http.StatusUnauthorized, rr1.Code)

	// 2. Bad header -> 401
	req2 := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-token")
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusUnauthorized, rr2.Code)

	// 3. Valid header -> 200
	req3 := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Telegram-Bot-Api-Secret-Token", "super-secret-token-123")
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)
	assert.Equal(t, http.StatusOK, rr3.Code)
}
