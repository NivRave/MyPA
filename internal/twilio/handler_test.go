package twilio

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
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

func TestHandleWebhook_SignatureVerification(t *testing.T) {
	pub := &mockPublisher{}
	authToken := "test-auth-token-12345"
	handler := NewHandler(pub, authToken)

	data := url.Values{}
	data.Set("Body", "Secured Twilio message")
	data.Set("From", "whatsapp:+1987654321")
	data.Set("MessageSid", "msg-sec-1")

	// 1. Missing signature -> 403
	reqMissing := httptest.NewRequest(http.MethodPost, "https://example.com/webhook/twilio", strings.NewReader(data.Encode()))
	reqMissing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrMissing := httptest.NewRecorder()
	handler.HandleWebhook(rrMissing, reqMissing)
	assert.Equal(t, http.StatusForbidden, rrMissing.Code)

	// 2. Bad signature -> 403
	reqBad := httptest.NewRequest(http.MethodPost, "https://example.com/webhook/twilio", strings.NewReader(data.Encode()))
	reqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBad.Header.Set("X-Twilio-Signature", "bad-signature")
	rrBad := httptest.NewRecorder()
	handler.HandleWebhook(rrBad, reqBad)
	assert.Equal(t, http.StatusForbidden, rrBad.Code)

	// 3. Valid signature -> 200
	reqValid := httptest.NewRequest(http.MethodPost, "https://example.com/webhook/twilio", strings.NewReader(data.Encode()))
	reqValid.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqValid.Header.Set("X-Forwarded-Proto", "https")
	reqValid.Header.Set("X-Forwarded-Host", "example.com")

	// Calculate correct signature
	// sorted params: Body=Secured Twilio message, From=whatsapp:+1987654321, MessageSid=msg-sec-1
	// payload: https://example.com/webhook/twilioBodySecured Twilio messageFromwhatsapp:+1987654321MessageSidmsg-sec-1
	sigPayload := "https://example.com/webhook/twilio" + "BodySecured Twilio message" + "Fromwhatsapp:+1987654321" + "MessageSidmsg-sec-1"
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(sigPayload))
	expectedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	reqValid.Header.Set("X-Twilio-Signature", expectedSig)
	rrValid := httptest.NewRecorder()
	handler.HandleWebhook(rrValid, reqValid)
	assert.Equal(t, http.StatusOK, rrValid.Code)
	require.Len(t, pub.Published, 1)
}
