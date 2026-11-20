package llm

import (
	"context"
	"github.com/nivik/mypa/internal/config"
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
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: "func", Response: map[string]any{}}}},
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: "func", Args: map[string]any{}}}},
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
	require.NotEmpty(t, resp.ToolCalls)
	assert.Equal(t, "create_calendar_event", resp.ToolCalls[0].Name)
	assert.Equal(t, "Test", resp.ToolCalls[0].Args["title"])
}

func TestClient_GenerateEmbedding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "EmbedContent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(embeddingsJSON(t, 1, 3, 0.1))
	}))
	defer ts.Close()

	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{
			Transport: &rewriteTransport{URL: ts.URL},
		}
	}

	client, err := NewClient(context.Background(), "mock-key", "", withHTTPClient)
	require.NoError(t, err)
	embedder, err := NewEmbedder(context.Background(), "mock-key", config.DefaultEmbeddingModel, 3, withHTTPClient)
	require.NoError(t, err)
	client.UseEmbedder(embedder)

	emb, err := client.GenerateEmbedding(context.Background(), "test text")
	require.NoError(t, err)
	require.Len(t, emb, 3)
	assert.Equal(t, float32(0.1), emb[0])
}

func TestClient_Chat_PromptBlocked(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"promptFeedback": {
				"blockReason": "PROHIBITED_CONTENT"
			},
			"candidates": []
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

	_, err = client.Chat(context.Background(), "sys", nil, "msg", nil, "")
	require.Error(t, err)
	var blockedErr *ContentBlockedError
	require.ErrorAs(t, err, &blockedErr)
	assert.Equal(t, "PROHIBITED_CONTENT", blockedErr.BlockReason)
}

func TestClient_Chat_CandidateSafetyBlocked(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"finishReason": "SAFETY"
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

	_, err = client.Chat(context.Background(), "sys", nil, "msg", nil, "")
	require.Error(t, err)
	var blockedErr *ContentBlockedError
	require.ErrorAs(t, err, &blockedErr)
	assert.Equal(t, "SAFETY", blockedErr.BlockReason)
}

func TestNewClient_NoKey(t *testing.T) {
	_, err := NewClient(context.Background(), "", "")
	require.Error(t, err)
}

func TestClient_ExtraTools(t *testing.T) {
	client := &Client{}
	assert.Empty(t, client.extraTools)

	decl1 := &genai.FunctionDeclaration{Name: "custom_tool_1"}
	decl2 := &genai.FunctionDeclaration{Name: "custom_tool_2"}

	client.SetExtraTools([]*genai.FunctionDeclaration{decl1})
	assert.Len(t, client.extraTools, 1)
	assert.Equal(t, "custom_tool_1", client.extraTools[0].Name)

	client.AddFunctionDeclarations(decl2)
	assert.Len(t, client.extraTools, 2)
	assert.Equal(t, "custom_tool_2", client.extraTools[1].Name)
}

func TestActiveTools_Context(t *testing.T) {
	ctx := context.Background()
	_, ok := ActiveToolsFromContext(ctx)
	assert.False(t, ok)

	decls := []*genai.FunctionDeclaration{
		{Name: "tool_alpha"},
		{Name: "tool_beta"},
	}
	ctxWithTools := WithActiveTools(ctx, decls)
	retrieved, ok := ActiveToolsFromContext(ctxWithTools)
	assert.True(t, ok)
	require.Len(t, retrieved, 2)
	assert.Equal(t, "tool_alpha", retrieved[0].Name)
	assert.Equal(t, "tool_beta", retrieved[1].Name)
}

func TestNoTools_Context(t *testing.T) {
	ctx := context.Background()
	assert.False(t, isNoTools(ctx))

	ctxNoTools := WithNoTools(ctx)
	assert.True(t, isNoTools(ctxNoTools))
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

// V4-09: a client created without a model uses the one default, config.DefaultGeminiModel, and
// asks the API for exactly that model.
func TestNewClient_WithoutAModelUsesTheConfiguredDefault(t *testing.T) {
	var requested string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}`))
	}))
	defer ts.Close()
	withHTTPClient := func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	}

	client, err := NewClient(context.Background(), "mock-key", "", withHTTPClient)
	require.NoError(t, err)
	assert.Equal(t, config.DefaultGeminiModel, client.Router().Primary().Model())

	_, err = client.Chat(context.Background(), "system", nil, "hi", nil, "")
	require.NoError(t, err)
	assert.Contains(t, requested, "models/"+config.DefaultGeminiModel+":", "the request must name the default model")

	explicit, err := NewClient(context.Background(), "mock-key", "another-model", withHTTPClient)
	require.NoError(t, err)
	assert.Equal(t, "another-model", explicit.Router().Primary().Model(), "an explicit model is used as given")
}
