package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_SearchWithAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/search", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"answer": "This is a summary answer",
			"results": []
		}`))
	}))
	defer ts.Close()

	client := NewClient("test-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	res, err := client.Search(context.Background(), "query")
	require.NoError(t, err)
	assert.Contains(t, res, "This is a summary answer")
}

func TestClient_SearchWithResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"answer": "",
			"results": [
				{"title": "Result 1", "url": "http://example.com/1", "content": "Content 1"},
				{"title": "Result 2", "url": "http://example.com/2", "content": "Content 2"}
			]
		}`))
	}))
	defer ts.Close()

	client := NewClient("test-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	res, err := client.Search(context.Background(), "query")
	require.NoError(t, err)
	assert.Contains(t, res, "Result 1")
	assert.Contains(t, res, "Content 1")
}

func TestClient_SearchNoResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"answer": "",
			"results": []
		}`))
	}))
	defer ts.Close()

	client := NewClient("test-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	res, err := client.Search(context.Background(), "query")
	require.NoError(t, err)
	assert.Contains(t, res, "No search results found.")
}

func TestClient_NoAPIKey(t *testing.T) {
	client := NewClient("")
	_, err := client.Search(context.Background(), "query")
	require.Error(t, err)
}

func TestClient_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient("test-key")
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}

	_, err := client.Search(context.Background(), "query")
	require.Error(t, err)
}

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
