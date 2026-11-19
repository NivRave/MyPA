package twilio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdversarial_SEC03_DownloadMedia_SchemeAndHostnameRejections(t *testing.T) {
	cfg := Config{
		AccountSID: "secret-account-sid-123",
		AuthToken:  "secret-auth-token-456",
	}
	client := NewClient(cfg)

	// Intercept any outgoing request: if untrusted URLs trigger an outgoing request, fail immediately
	unwantedRequestMade := false
	client.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		unwantedRequestMade = true
		return nil, http.ErrAbortHandler
	})

	adversarialURLs := []struct {
		name        string
		targetURL   string
		expectedErr string
	}{
		{"PlainHTTP_Attacker", "http://attacker.com/media", "HTTPS is strictly required"},
		{"PlainHTTP_Twilio", "http://api.twilio.com/media", "HTTPS is strictly required"},
		{"PlainHTTP_TwilioCDN", "http://media.twiliocdn.com/media", "HTTPS is strictly required"},
		{"UntrustedDomain_EvilCom", "https://evil.com/media", "untrusted media URL hostname"},
		{"UntrustedDomain_AttackerCom", "https://attacker.com/media", "untrusted media URL hostname"},
		{"DomainSpoofing_SuffixTwilio", "https://api.twilio.com.attacker.com/media", "untrusted media URL hostname"},
		{"DomainSpoofing_PrefixTwilio", "https://attacker.api.twilio.com/media", "untrusted media URL hostname"},
		{"DomainSpoofing_SubdomainCDN", "https://evil.media.twiliocdn.com/media", "untrusted media URL hostname"},
		{"FileScheme", "file:///etc/passwd", "HTTPS is strictly required"},
		{"FTP_Scheme", "ftp://api.twilio.com/media", "HTTPS is strictly required"},
	}

	for _, tc := range adversarialURLs {
		t.Run(tc.name, func(t *testing.T) {
			unwantedRequestMade = false
			data, err := client.DownloadMedia(context.Background(), tc.targetURL)
			assert.Error(t, err)
			assert.Nil(t, data)
			assert.Contains(t, err.Error(), tc.expectedErr)
			assert.False(t, unwantedRequestMade, "No network request should have been dispatched for invalid target: %s", tc.targetURL)
		})
	}
}

func TestAdversarial_SEC03_DownloadMedia_BasicAuthAttachedOnlyToTrustedHosts(t *testing.T) {
	var capturedUser, capturedPass string
	var authPresent bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUser, capturedPass, authPresent = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("valid-media-payload"))
	}))
	defer ts.Close()

	cfg := Config{
		AccountSID: "verified-sid",
		AuthToken:  "verified-token",
	}
	client := NewClient(cfg)
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	// 1. Trusted host api.twilio.com -> BasicAuth attached
	capturedUser, capturedPass, authPresent = "", "", false
	data, err := client.DownloadMedia(context.Background(), "https://api.twilio.com/media/file1")
	require.NoError(t, err)
	assert.Equal(t, []byte("valid-media-payload"), data)
	assert.True(t, authPresent)
	assert.Equal(t, "verified-sid", capturedUser)
	assert.Equal(t, "verified-token", capturedPass)

	// 2. Trusted host media.twiliocdn.com -> BasicAuth attached
	capturedUser, capturedPass, authPresent = "", "", false
	data, err = client.DownloadMedia(context.Background(), "https://media.twiliocdn.com/media/file2")
	require.NoError(t, err)
	assert.Equal(t, []byte("valid-media-payload"), data)
	assert.True(t, authPresent)
	assert.Equal(t, "verified-sid", capturedUser)
	assert.Equal(t, "verified-token", capturedPass)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
