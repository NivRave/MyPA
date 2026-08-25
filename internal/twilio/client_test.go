package twilio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_SendMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/2010-04-01/Accounts/test-sid/Messages.json", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "test-sid", user)
		assert.Equal(t, "test-token", pass)

		err := r.ParseForm()
		require.NoError(t, err)
		assert.Equal(t, "whatsapp:+1234567890", r.FormValue("To"))
		assert.Equal(t, "whatsapp:+0987654321", r.FormValue("From"))
		assert.Equal(t, "Hello World", r.FormValue("Body"))

		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	cfg := config.TwilioConfig{
		AccountSID: "test-sid",
		AuthToken:  "test-token",
		FromNumber: "whatsapp:+0987654321",
	}
	client := NewClient(cfg)
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SendMessage(context.Background(), "+1234567890", "Hello World")
	require.NoError(t, err)
}

func TestClient_DownloadMedia(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/media/123", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "test-sid", user)
		assert.Equal(t, "test-token", pass)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("media content"))
	}))
	defer ts.Close()

	cfg := config.TwilioConfig{
		AccountSID: "test-sid",
		AuthToken:  "test-token",
	}
	client := NewClient(cfg)
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	// We pass the path as URL and the rewriteTransport changes scheme/host
	content, err := client.DownloadMedia(context.Background(), "http://api.twilio.com/media/123")
	require.NoError(t, err)
	assert.Equal(t, []byte("media content"), content)
}

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
