package web_research

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/netguard"
	"github.com/nivik/mypa/internal/orchestrator"
)

type mockTavilyClient struct {
	lastQuery string
}

func (m *mockTavilyClient) Search(ctx context.Context, query string) (string, error) {
	m.lastQuery = query
	return "Search results for: " + query, nil
}

type mockLLMClient struct {
	lastPrompt string
}

func (m *mockLLMClient) Chat(ctx context.Context, systemPrompt string, history []models.ChatMessage, userMessage string, photoData []byte, photoMimeType string) (*llm.Response, error) {
	m.lastPrompt = userMessage
	return &llm.Response{Text: "Summary of page"}, nil
}

func (m *mockLLMClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}

func TestWebResearch_Operations(t *testing.T) {
	netguard.SetAllowLoopbackForTesting(true)
	defer netguard.SetAllowLoopbackForTesting(false)
	tavilyMock := &mockTavilyClient{}
	llmMock := &mockLLMClient{}

	mod := NewModule(WithClient(tavilyMock))
	if mod.Name() != "web_research" {
		t.Errorf("expected name web_research, got %s", mod.Name())
	}
	if len(mod.Declarations()) != 2 {
		t.Errorf("expected 2 declarations, got %d", len(mod.Declarations()))
	}

	callCtx := orchestrator.CallContext{
		User: &models.User{PlatformID: "user_web"},
		LLM:  llmMock,
	}

	// 1. Search Web
	res, err := mod.Execute(context.Background(), callCtx, "search_web", map[string]interface{}{
		"query": "Go 1.24 features",
	})
	if err != nil || !strings.Contains(res, "Go 1.24 features") {
		t.Fatalf("search_web failed: res=%s, err=%v", res, err)
	}

	// 2. Fetch Webpage
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>Article content here</p></article></body></html>`))
	}))
	defer ts.Close()

	res, err = mod.Execute(context.Background(), callCtx, "fetch_webpage", map[string]interface{}{
		"url": ts.URL,
	})
	if err != nil || res != "Summary of page" {
		t.Fatalf("fetch_webpage failed: res=%s, err=%v", res, err)
	}
	if !strings.Contains(llmMock.lastPrompt, "<untrusted_external_content") {
		t.Errorf("expected untrusted wrapping in LLM prompt: %s", llmMock.lastPrompt)
	}
}
