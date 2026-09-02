package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestClient_Chat(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "generateContent")
		
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"content": {
						"parts": [
							{"text": "mocked response text"}
						],
						"role": "model"
					}
				}
			]
		}`))
	}))
	defer ts.Close()

	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{
			Transport: &rewriteTransport{URL: ts.URL},
		}
	}

	client, err := NewClient(context.Background(), "mock-key", "gemini-test", withHTTPClient)
	require.NoError(t, err)

	history := []models.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "function", ToolResponse: &models.FunctionResponse{Name: "func", Response: map[string]any{}}},
		{Role: "assistant", ToolCall: &models.FunctionCall{Name: "func", Args: map[string]any{}}},
	}

	resp, err := client.Chat(context.Background(), "system prompt", history, "new user message", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "mocked response text", resp.Text)
}

func TestClient_Chat_ToolCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"content": {
						"parts": [
							{
								"functionCall": {
									"name": "create_calendar_event",
									"args": {"title": "Test"}
								}
							}
						],
						"role": "model"
					}
				}
			]
		}`))
	}))
	defer ts.Close()

	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{
			Transport: &rewriteTransport{URL: ts.URL},
		}
	}

	client, err := NewClient(context.Background(), "mock-key", "gemini-test", withHTTPClient)
	require.NoError(t, err)

	resp, err := client.Chat(context.Background(), "sys", nil, "msg", nil, "")
	require.NoError(t, err)
	require.NotNil(t, resp.ToolCall)
	assert.Equal(t, "create_calendar_event", resp.ToolCall.Name)
	assert.Equal(t, "Test", resp.ToolCall.Args["title"])
}

func TestClient_GenerateEmbedding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "EmbedContent")
		
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"embeddings": [
				{
					"values": [0.1, 0.2, 0.3]
				}
			]
		}`))
	}))
	defer ts.Close()

	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{
			Transport: &rewriteTransport{URL: ts.URL},
		}
	}

	client, err := NewClient(context.Background(), "mock-key", "", withHTTPClient)
	require.NoError(t, err)

	emb, err := client.GenerateEmbedding(context.Background(), "test text")
	require.NoError(t, err)
	require.Len(t, emb, 3)
	assert.Equal(t, float32(0.1), emb[0])
}

func TestNewClient_NoKey(t *testing.T) {
	_, err := NewClient(context.Background(), "", "")
	require.Error(t, err)
}

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// genai uses https://generativelanguage.googleapis.com
	// we rewrite it to our test server
	req.URL.Scheme = "http"
	req.URL.Host = t.URL[7:] // strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
