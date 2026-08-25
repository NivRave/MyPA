package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_SendMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/sendMessage", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	
	// Override the URL directly since it's constructed in the method. 
	// Wait, the URL is hardcoded in the method as "https://api.telegram.org/...".
	// We should allow injecting a base URL or override http.RoundTripper.
	
	// Since we can't easily override the URL without changing the code, let's use a custom Transport.
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SendMessage(context.Background(), "chat-123", "Hello World")
	require.NoError(t, err)
}

func TestClient_SendMessage_Fallback(t *testing.T) {
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			// Fail the first attempt (HTML formatting issue)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Second attempt without HTML should succeed
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	err := client.SendMessage(context.Background(), "chat-123", "Bad HTML <")
	require.NoError(t, err)
	assert.Equal(t, 2, attempt)
}

func TestClient_GetFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/getFile", r.URL.Path)
		assert.Equal(t, "file_id=12345", r.URL.RawQuery)
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": true, "result": {"file_path": "music/file.ogg"}}`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	path, err := client.GetFile(context.Background(), "12345")
	require.NoError(t, err)
	assert.Equal(t, "music/file.ogg", path)
}

func TestClient_DownloadFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/file/bottest-token/music/file.ogg", r.URL.Path)
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`file-content`))
	}))
	defer ts.Close()

	client := NewClient("test-token")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	content, err := client.DownloadFile(context.Background(), "music/file.ogg")
	require.NoError(t, err)
	assert.Equal(t, []byte("file-content"), content)
}

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
