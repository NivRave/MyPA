package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_TranscribeAudio(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/openai/v1/audio/transcriptions", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.True(t, strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data"))

		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		assert.Equal(t, "whisper-large-v3-turbo", r.FormValue("model"))

		file, header, err := r.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		assert.Equal(t, "test.ogg", header.Filename)

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text": "Hello this is a test"}`))
	}))
	defer ts.Close()

	client := NewClient("test-api-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	audioData := []byte("fake-audio-data")
	text, err := client.TranscribeAudio(context.Background(), audioData, "test.ogg")
	require.NoError(t, err)
	assert.Equal(t, "Hello this is a test", text)
}

func TestClient_TranscribeAudio_NoAPIKey(t *testing.T) {
	client := NewClient("")
	_, err := client.TranscribeAudio(context.Background(), []byte("data"), "test.ogg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key is not configured")
}

func TestClient_TranscribeAudio_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "Internal Server Error"}`))
	}))
	defer ts.Close()

	client := NewClient("test-api-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	_, err := client.TranscribeAudio(context.Background(), []byte("data"), "test.ogg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "groq API error")
}

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
