package twilio

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signedRequest(t *testing.T, signedURL, requestURL, token string, headers map[string]string) *http.Request {
	t.Helper()
	form := url.Values{"From": {"whatsapp:+12025550101"}, "Body": {"hi"}, "MessageSid": {"SM1"}}
	payload := signedURL + "Bodyhi" + "Fromwhatsapp:+12025550101" + "MessageSidSM1"
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(payload))
	req := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// V4-40: Twilio signs the address it was given, which is the public one. With PUBLIC_URL set the
// handler checks the signature against it, so it does not depend on how each tunnel or proxy
// forwards the host and scheme (some send no X-Forwarded-Proto at all).
func TestHandleWebhook_VerifiesAgainstThePublicAddress(t *testing.T) {
	const token = "tok"
	const public = "https://mypa.example.com"

	t.Run("a proxy that forwards nothing", func(t *testing.T) {
		pub := &mockPublisher{}
		req := signedRequest(t, public+WebhookPath, "http://app:8000"+WebhookPath, token, nil)
		rr := httptest.NewRecorder()
		NewHandler(pub, token).WithPublicURL(public).HandleWebhook(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Len(t, pub.Published, 1)
	})
	t.Run("without the public address the same request fails closed", func(t *testing.T) {
		pub := &mockPublisher{}
		req := signedRequest(t, public+WebhookPath, "http://app:8000"+WebhookPath, token, nil)
		rr := httptest.NewRecorder()
		NewHandler(pub, token).HandleWebhook(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Empty(t, pub.Published)
	})
	t.Run("forwarded headers cannot move the signed address", func(t *testing.T) {
		pub := &mockPublisher{}
		other := "https://elsewhere.example"
		req := signedRequest(t, other+WebhookPath, "http://app:8000"+WebhookPath, token,
			map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "elsewhere.example"})
		rr := httptest.NewRecorder()
		NewHandler(pub, token).WithPublicURL(public).HandleWebhook(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Empty(t, pub.Published)
	})
}

func TestLoadConfig_DerivesTheAddressesFromThePublicURL(t *testing.T) {
	getenv := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	keys := map[string]string{"TWILIO_ACCOUNT_SID": "AC1", "TWILIO_AUTH_TOKEN": "tok"}

	server := LoadConfig(getenv(keys), deploy.Load(getenv(map[string]string{"INGRESS": "ngrok", "PUBLIC_URL": "https://a.ngrok-free.dev"})))
	assert.Equal(t, "https://a.ngrok-free.dev/webhook/twilio/status", server.StatusCallbackURL, "delivery reports are on wherever there is an address")
	assert.Equal(t, "https://a.ngrok-free.dev", server.PublicURL)

	local := LoadConfig(getenv(keys), deploy.Load(getenv(nil)))
	assert.Empty(t, local.StatusCallbackURL)
	assert.Empty(t, local.PublicURL)
	require.True(t, NewAdapter(local).Configured())
	assert.True(t, NewAdapter(local).Ingress().NeedsPublicAddress(), "WhatsApp always needs a public address")
	assert.False(t, NewAdapter(Config{}).Configured())
}
