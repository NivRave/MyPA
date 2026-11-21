package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/config"
	"google.golang.org/genai"
)

// The OpenAI-compatible chat completions endpoints.
const (
	OpenAIURL = "https://api.openai.com/v1/chat/completions"
	XAIURL    = "https://api.x.ai/v1/chat/completions"
	// ollamaPath is Ollama's OpenAI-compatible endpoint, under OLLAMA_BASE_URL.
	ollamaPath = "/v1/chat/completions"
)

// openAIProvider is the OpenAI-compatible adapter. One wire format serves three providers:
// OpenAI, xAI's Grok and a local Ollama.
type openAIProvider struct {
	name, key, model, url string
	local                 bool
	// pdf reports whether the endpoint reads a PDF sent as a file part, as the provider declares
	// (CapabilitiesOf): OpenAI does; Grok and Ollama do not, and get a note instead.
	pdf  bool
	http *http.Client
}

// NewOpenAIProvider builds the adapter for OpenAI.
func NewOpenAIProvider(apiKey, model string, client *http.Client) (Provider, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("openai needs OPENAI_API_KEY and OPENAI_MODEL")
	}
	return &openAIProvider{name: config.ProviderOpenAI, key: apiKey, model: model, url: OpenAIURL, pdf: readsPDF(config.ProviderOpenAI), http: orDefault(client)}, nil
}

// NewGrokProvider builds the adapter for xAI's Grok.
func NewGrokProvider(apiKey, model string, client *http.Client) (Provider, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("grok needs XAI_API_KEY and XAI_MODEL")
	}
	return &openAIProvider{name: config.ProviderGrok, key: apiKey, model: model, url: XAIURL, pdf: readsPDF(config.ProviderGrok), http: orDefault(client)}, nil
}

// NewOllamaProvider builds the adapter for a local Ollama server. It sends no key.
func NewOllamaProvider(baseURL, model string, client *http.Client) (Provider, error) {
	if baseURL == "" || model == "" {
		return nil, fmt.Errorf("ollama needs OLLAMA_BASE_URL and OLLAMA_MODEL")
	}
	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + ollamaPath
	return &openAIProvider{name: config.ProviderOllama, model: model, url: url, local: true, pdf: readsPDF(config.ProviderOllama), http: orDefault(client)}, nil
}

func readsPDF(provider string) bool { return CapabilitiesOf(provider).PDF == Supported }

func orDefault(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{Timeout: requestTimeout}
	}
	return client
}

func (p *openAIProvider) Name() string  { return p.name }
func (p *openAIProvider) Model() string { return p.model }
func (p *openAIProvider) Local() bool   { return p.local }

// openAIBody builds the request body. It carries no sampling parameter (BL-29).
func openAIBody(model string, pdf bool, req Request) map[string]any {
	var messages []map[string]any
	if req.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, t := range buildTurns(req) {
		if t.Role == "assistant" {
			msg := map[string]any{"role": "assistant"}
			var text strings.Builder
			var calls []map[string]any
			for _, p := range t.Parts {
				if p.Call != nil {
					args, err := json.Marshal(p.Call.Args)
					if err != nil || p.Call.Args == nil {
						args = []byte("{}")
					}
					calls = append(calls, map[string]any{"id": p.CallID, "type": "function",
						"function": map[string]any{"name": p.Call.Name, "arguments": string(args)}})
				} else if p.Text != "" {
					text.WriteString(p.Text)
				}
			}
			msg["content"] = text.String()
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
			continue
		}
		// A user turn: each tool result is a "tool" message of its own, and must directly follow
		// the assistant message that made the call; the rest is one user message after them.
		var content []map[string]any
		for _, p := range t.Parts {
			switch {
			case p.Result != nil:
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": p.ResultID, "content": resultJSON(p.Result)})
			case len(p.Data) > 0:
				content = append(content, openAIAttachment(p.Data, p.MimeType, pdf))
			case p.Text != "":
				content = append(content, map[string]any{"type": "text", "text": p.Text})
			}
		}
		if len(content) > 0 {
			messages = append(messages, map[string]any{"role": "user", "content": content})
		}
	}
	body := map[string]any{"model": model, "messages": messages}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, d := range req.Tools {
			if d == nil || d.Name == "" {
				continue
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": d.Name, "description": d.Description, "parameters": toolSchema(d)}})
		}
		body["tools"] = tools
	}
	return body
}

// openAIAttachment is an image part (a data URL), a PDF file part where the endpoint reads one, or
// a note in place of what it cannot read.
func openAIAttachment(data []byte, mime string, pdf bool) map[string]any {
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	switch {
	case strings.HasPrefix(mime, "image/"):
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}}
	case mime == "application/pdf" && pdf:
		return map[string]any{"type": "file", "file": map[string]any{"filename": "document.pdf", "file_data": dataURL}}
	}
	return map[string]any{"type": "text", "text": unsupportedAttachment(mime)}
}

type openAIReply struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   *string `json:"content"`
			Refusal   *string `json:"refusal"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	// Usage is the call's counts; completion_tokens includes a reasoning model's reasoning.
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *openAIProvider) Chat(ctx context.Context, req Request) (*Response, error) {
	payload, err := json.Marshal(openAIBody(p.model, p.pdf, req))
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", p.name, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", p.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.key)
	}

	res, err := p.http.Do(httpReq)
	if err != nil {
		return nil, transportError(ctx, p.name, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, transportError(ctx, p.name, err)
	}
	if res.StatusCode != http.StatusOK {
		slog.Error("llm api error", "provider", p.name, "status", res.StatusCode)
		return nil, statusError(p.name, res.StatusCode, body)
	}
	var reply openAIReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("%s reply: %w", p.name, err)
	}
	if len(reply.Choices) == 0 {
		return nil, fmt.Errorf("%s returned no choices", p.name)
	}
	choice := reply.Choices[0]
	if choice.FinishReason == "content_filter" {
		return nil, &ContentBlockedError{BlockReason: "content_filter", Provider: p.name}
	}
	if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
		return nil, &ContentBlockedError{BlockReason: "refusal", Provider: p.name}
	}
	out := &Response{Provider: p.name, Model: p.model,
		Usage: Usage{InputTokens: reply.Usage.PromptTokens, OutputTokens: reply.Usage.CompletionTokens}}
	for _, tc := range choice.Message.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("%s returned tool arguments that are not a JSON object for %s: %w", p.name, tc.Function.Name, err)
			}
		}
		slog.Info("LLM invoked function", "name", tc.Function.Name)
		out.ToolCalls = append(out.ToolCalls, &genai.FunctionCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	if len(out.ToolCalls) == 0 && choice.Message.Content != nil {
		out.Text = *choice.Message.Content
	}
	return out, nil
}
