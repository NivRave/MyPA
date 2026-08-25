package twilio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPublisher struct {
	Published []any
}

func (m *mockPublisher) Publish(ctx context.Context, v any) error {
	m.Published = append(m.Published, v)
	return nil
}

func TestHandleWebhook_PostMethod(t *testing.T) {
	pub := &mockPublisher{}
	handler := NewHandler(pub)

	data := url.Values{}
	data.Set("From", "whatsapp:+123456789")
	data.Set("Body", "Test message")
	data.Set("MessageSid", "msg-123")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.Published, 1)

	msg, ok := pub.Published[0].(models.Message)
	require.True(t, ok)
	assert.Equal(t, "msg-123", msg.ID)
	assert.Equal(t, "+123456789", msg.UserID) // Notice the whatsapp: prefix is stripped
	assert.Equal(t, "whatsapp", msg.Source)
	assert.Equal(t, "Test message", msg.Text)
}

func TestHandleWebhook_WrongMethod(t *testing.T) {
	pub := &mockPublisher{}
	handler := NewHandler(pub)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Len(t, pub.Published, 0)
}
