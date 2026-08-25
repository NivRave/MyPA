package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
	r := setupRouter(pub)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestGatewayTelegramWebhook_Success(t *testing.T) {
	pub := new(MockEventPublisher)
	r := setupRouter(pub)

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
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	pub.AssertExpectations(t)
}

func TestGatewayTelegramWebhook_InvalidJSON(t *testing.T) {
	pub := new(MockEventPublisher)
	r := setupRouter(pub)

	payload := []byte(`{"update_id": 12345, bad json`)

	req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// In the real code we return 200 on parse error to avoid telegram retries
	assert.Equal(t, http.StatusOK, rr.Code)
}
