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

const (
	// AnthropicURL is the Messages API endpoint.
	AnthropicURL = "https://api.anthropic.com/v1/messages"
	// anthropicVersion is the API version header the request format below follows.
	anthropicVersion = "2023-06-01"
	// anthropicMaxTokens bounds a reply. The API requires the field; it is a length limit, not a
	// sampling parameter, and a family assistant's replies are far shorter.
	anthropicMaxTokens = 4096
)

// anthropicImageTypes are the image types the Messages API accepts.
var anthropicImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// anthropicProvider is the Anthropic (Claude) adapter.
type anthropicProvider struct {
	key, model, url string
	http            *http.Client
}

// NewAnthropicProvider builds the Anthropic adapter. A nil client gets the default timeout.
func NewAnthropicProvider(apiKey, model string, client *http.Client) (Provider, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("anthropic needs ANTHROPIC_API_KEY and ANTHROPIC_MODEL")
	}
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return &anthropicProvider{key: apiKey, model: model, url: AnthropicURL, http: client}, nil
}

func (p *anthropicProvider) Name() string  { return config.ProviderAnthropic }
func (p *anthropicProvider) Model() string { return p.model }
func (p *anthropicProvider) Local() bool   { return false }

// anthropicBody builds the request body. It carries no sampling parameter (BL-29).
func anthropicBody(model string, req Request) map[string]any {
	var messages []map[string]any
	for _, t := range buildTurns(req) {
		var blocks []map[string]any
		for _, p := range t.Parts {
			switch {
			case p.Call != nil:
				args := p.Call.Args
				if args == nil {
					args = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": p.CallID, "name": p.Call.Name, "input": args})
			case p.Result != nil:
				blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": p.ResultID, "content": resultJSON(p.Result)})
			case len(p.Data) > 0:
				blocks = append(blocks, anthropicAttachment(p.Data, p.MimeType))
			case p.Text != "":
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		}
		// A tool result must come first in its user message.
		blocks = resultsFirst(blocks)
		messages = append(messages, map[string]any{"role": t.Role, "content": blocks})
	}
	body := map[string]any{"model": model, "max_tokens": anthropicMaxTokens, "messages": messages}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, d := range req.Tools {
			if d == nil || d.Name == "" {
				continue
			}
			tools = append(tools, map[string]any{"name": d.Name, "description": d.Description, "input_schema": toolSchema(d)})
		}
		body["tools"] = tools
	}
	return body
}

func resultsFirst(blocks []map[string]any) []map[string]any {
	var results, rest []map[string]any
	for _, b := range blocks {
		if b["type"] == "tool_result" {
			results = append(results, b)
		} else {
			rest = append(rest, b)
		}
	}
	return append(results, rest...)
}

// anthropicAttachment is an image or PDF block. A type the API does not take is replaced by a
// note, so the model can tell the person instead of the whole request failing.
func anthropicAttachment(data []byte, mime string) map[string]any {
	encoded := base64.StdEncoding.EncodeToString(data)
	switch {
	case anthropicImageTypes[mime]:
		return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mime, "data": encoded}}
	case mime == "application/pdf":
		return map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": mime, "data": encoded}}
	}
	return map[string]any{"type": "text", "text": unsupportedAttachment(mime)}
}

// unsupportedAttachment is the note sent in place of an attachment a provider cannot read.
func unsupportedAttachment(mime string) string {
	return fmt.Sprintf("[An attachment of type %s was sent, but this model cannot read it. Tell the person so.]", mime)
}

type anthropicReply struct {
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text"`
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
}

// usage maps Anthropic's counts onto Usage: input_tokens leaves out what was written to or read
// from the prompt cache, so both are added to the input.
func (r anthropicReply) usage() Usage {
	u := r.Usage
	return Usage{InputTokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens, OutputTokens: u.OutputTokens}
}

func (p *anthropicProvider) Chat(ctx context.Context, req Request) (*Response, error) {
	payload, err := json.Marshal(anthropicBody(p.model, req))
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	res, err := p.http.Do(httpReq)
	if err != nil {
		return nil, transportError(ctx, p.Name(), err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, transportError(ctx, p.Name(), err)
	}
	if res.StatusCode != http.StatusOK {
		slog.Error("anthropic api error", "status", res.StatusCode)
		return nil, statusError(p.Name(), res.StatusCode, body)
	}
	var reply anthropicReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("anthropic reply: %w", err)
	}
	if reply.StopReason == "refusal" {
		return nil, &ContentBlockedError{BlockReason: "refusal", Provider: p.Name()}
	}
	out := &Response{Provider: p.Name(), Model: p.model, Usage: reply.usage()}
	var text strings.Builder
	for _, c := range reply.Content {
		switch c.Type {
		case "tool_use":
			slog.Info("LLM invoked function", "name", c.Name)
			out.ToolCalls = append(out.ToolCalls, &genai.FunctionCall{ID: c.ID, Name: c.Name, Args: c.Input})
		case "text":
			text.WriteString(c.Text)
		}
	}
	if len(out.ToolCalls) == 0 {
		out.Text = text.String()
	}
	return out, nil
}
