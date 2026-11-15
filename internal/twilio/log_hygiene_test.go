package twilio

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// P3-3: the Twilio webhook must not log message text at INFO.
func TestHandleWebhook_DoesNotLogMessageText(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	token := "tok"
	h := NewHandler(&mockPublisher{}, token)
	form := url.Values{"From": {"whatsapp:+1555"}, "Body": {"my bank PIN is 4321"}, "MessageSid": {"SM9"}}
	req := httptest.NewRequest(http.MethodPost, "http://example.com/wh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte("http://example.com/wh" + "Bodymy bank PIN is 4321" + "Fromwhatsapp:+1555" + "MessageSidSM9"))
	req.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	rr := httptest.NewRecorder()
	h.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, buf.String(), "4321")
	assert.Contains(t, buf.String(), `"text_len"`)
}
