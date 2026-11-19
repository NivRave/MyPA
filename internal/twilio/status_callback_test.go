package twilio

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-8: Twilio reports delivery asynchronously. A reply can be accepted by the REST API and then
// fail (e.g. 63015, recipient left the sandbox) — that must reach the operator's logs.

const statusURL = "https://gw.example/webhook/twilio/status"

func signForm(token, fullURL string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(fullURL)
	for _, k := range keys {
		sb.WriteString(k + form.Get(k))
	}
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(sb.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func statusRequest(form url.Values, sig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, statusURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "gw.example")
	if sig != "" {
		req.Header.Set("X-Twilio-Signature", sig)
	}
	return req
}

func captureStatusLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func failedForm(code string) url.Values {
	return url.Values{
		"MessageSid": {"SM123"}, "MessageStatus": {"failed"}, "ErrorCode": {code},
		"To": {"whatsapp:+15550001111"}, "AccountSid": {"AC1"},
	}
}

func TestStatusCallback_RequiresValidSignature(t *testing.T) {
	pub := &mockPublisher{}
	form := failedForm("63015")
	for name, h := range map[string]*Handler{
		"no auth token configured": NewHandler(pub),
		"auth token configured":    NewHandler(pub, "tok"),
	} {
		t.Run(name, func(t *testing.T) {
			for _, sig := range []string{"", "bad", signForm("", statusURL, form)} {
				rr := httptest.NewRecorder()
				h.HandleStatusCallback(rr, statusRequest(form, sig))
				assert.Equal(t, http.StatusForbidden, rr.Code, "sig=%q", sig)
			}
		})
	}
	assert.Empty(t, pub.Published, "a status callback never publishes a message")
}

func TestStatusCallback_FailedDeliveryIsWarnedWithHint(t *testing.T) {
	logs := captureStatusLogs(t)
	form := failedForm("63015")
	rr := httptest.NewRecorder()
	NewHandler(&mockPublisher{}, "tok").HandleStatusCallback(rr, statusRequest(form, signForm("tok", statusURL, form)))

	assert.Equal(t, http.StatusOK, rr.Code)
	out := logs.String()
	assert.Contains(t, out, `"level":"WARN"`)
	assert.Contains(t, out, `"message_sid":"SM123"`)
	assert.Contains(t, out, `"error_code":"63015"`)
	assert.Contains(t, out, "sandbox", "known codes carry an operator hint")
	assert.Contains(t, out, `"user_id":"+15550001111"`, "same user_id form as inbound logs")
}

func TestStatusCallback_UndeliveredUnknownCodeIsWarned(t *testing.T) {
	logs := captureStatusLogs(t)
	form := failedForm("99999")
	form.Set("MessageStatus", "undelivered")
	rr := httptest.NewRecorder()
	NewHandler(&mockPublisher{}, "tok").HandleStatusCallback(rr, statusRequest(form, signForm("tok", statusURL, form)))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, logs.String(), `"level":"WARN"`)
	assert.Contains(t, logs.String(), `"error_code":"99999"`)
}

func TestStatusCallback_SuccessfulStatusesAreNotWarnings(t *testing.T) {
	for _, status := range []string{"queued", "sent", "delivered", "read"} {
		t.Run(status, func(t *testing.T) {
			logs := captureStatusLogs(t)
			form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {status}, "To": {"whatsapp:+15550001111"}}
			rr := httptest.NewRecorder()
			NewHandler(&mockPublisher{}, "tok").HandleStatusCallback(rr, statusRequest(form, signForm("tok", statusURL, form)))
			assert.Equal(t, http.StatusOK, rr.Code)
			assert.NotContains(t, logs.String(), `"level":"WARN"`)
		})
	}
}

func TestClient_SendMessage_StatusCallback(t *testing.T) {
	for _, tc := range []struct{ name, configured string }{
		{"set when configured", "https://gw.example/webhook/twilio/status"},
		{"omitted when not configured", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, seen := "", false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				_, seen = r.PostForm["StatusCallback"]
				got = r.PostForm.Get("StatusCallback")
				w.WriteHeader(http.StatusCreated)
			}))
			defer ts.Close()
			client := NewClient(Config{AccountSID: "sid", AuthToken: "tok", FromNumber: "whatsapp:+1", StatusCallbackURL: tc.configured})
			client.httpClient.Transport = &rewriteTransport{URL: ts.URL}
			require.NoError(t, client.SendMessage(context.Background(), "+2", "hi"))
			assert.Equal(t, tc.configured != "", seen)
			assert.Equal(t, tc.configured, got)
		})
	}
}
