package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nivik/mypa/internal/config"
	"google.golang.org/genai"
)

// requestTimeout bounds one chat request when the caller gives no HTTP client of its own.
const requestTimeout = 90 * time.Second

// geminiProvider is the Gemini adapter, on Google's genai SDK.
type geminiProvider struct {
	client *genai.Client
	model  string
}

// NewGeminiProvider builds the Gemini adapter. An empty model is the default chat model
// (config.DefaultGeminiModel, V4-09).
func NewGeminiProvider(ctx context.Context, apiKey, model string, opts ...func(*genai.ClientConfig)) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini api key is required")
	}
	if model == "" {
		model = config.DefaultGeminiModel // the one definition of the default (V4-09)
	}
	cfg := &genai.ClientConfig{APIKey: apiKey}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}
	return &geminiProvider{client: client, model: model}, nil
}

func (p *geminiProvider) Name() string  { return config.ProviderGemini }
func (p *geminiProvider) Model() string { return p.model }
func (p *geminiProvider) Local() bool   { return false }

// generateConfig is the request configuration. It sets no sampling parameter (BL-29): the default
// model ignores them and later Gemini generations reject them with HTTP 400.
func generateConfig(req Request) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(req.System)}},
	}
	if len(req.Tools) > 0 {
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: req.Tools}}
	}
	return cfg
}

// geminiContents converts the history and the new message into Gemini contents.
func geminiContents(req Request) []*genai.Content {
	var contents []*genai.Content
	for _, msg := range req.History {
		// Our roles ("user", "assistant", "function") map to Gemini's "user" and "model"; Gemini
		// takes function responses as a user turn.
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "function" {
			role = "user"
		}
		var parts []*genai.Part
		for _, tc := range msg.ToolCalls {
			parts = append(parts, genai.NewPartFromFunctionCall(tc.Name, tc.Args))
		}
		for _, tr := range msg.ToolResponses {
			parts = append(parts, genai.NewPartFromFunctionResponse(tr.Name, tr.Response))
		}
		if msg.Content != "" {
			parts = append(parts, genai.NewPartFromText(msg.Content))
		}
		if len(msg.PhotoData) > 0 && msg.PhotoMimeType != "" {
			parts = append(parts, genai.NewPartFromBytes(msg.PhotoData, msg.PhotoMimeType))
		}
		if len(parts) > 0 {
			contents = append(contents, &genai.Content{Role: role, Parts: parts})
		}
	}
	var newParts []*genai.Part
	if req.Message != "" {
		newParts = append(newParts, genai.NewPartFromText(req.Message))
	}
	if len(req.PhotoData) > 0 && req.PhotoMimeType != "" {
		newParts = append(newParts, genai.NewPartFromBytes(req.PhotoData, req.PhotoMimeType))
	}
	if len(newParts) == 0 {
		newParts = append(newParts, genai.NewPartFromText(" ")) // Fallback to avoid empty parts
	}
	return append(contents, &genai.Content{Role: "user", Parts: newParts})
}

// geminiUsage maps Gemini's counts onto Usage: the prompt and the tool-use prompt are input; the
// candidates and a thinking model's thoughts, which Gemini bills as output, are output.
func geminiUsage(m *genai.GenerateContentResponseUsageMetadata) Usage {
	if m == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:  int(m.PromptTokenCount) + int(m.ToolUsePromptTokenCount),
		OutputTokens: int(m.CandidatesTokenCount) + int(m.ThoughtsTokenCount),
	}
}

func (p *geminiProvider) Chat(ctx context.Context, req Request) (*Response, error) {
	resp, err := p.client.Models.GenerateContent(ctx, p.model, geminiContents(req), generateConfig(req))
	if err != nil {
		slog.Error("gemini api error", "error", err)
		var apiErr genai.APIError
		if errors.As(err, &apiErr) {
			if unavailableStatus(apiErr.Code) {
				return nil, &UnavailableError{Provider: p.Name(), Status: apiErr.Code, Err: err}
			}
			return nil, fmt.Errorf("failed to generate content: %w", err)
		}
		return nil, transportError(ctx, p.Name(), err)
	}

	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		return nil, &ContentBlockedError{BlockReason: string(resp.PromptFeedback.BlockReason), Provider: p.Name()}
	}
	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates returned from model")
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
			switch candidate.FinishReason {
			case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT":
				return nil, &ContentBlockedError{BlockReason: string(candidate.FinishReason), Provider: p.Name()}
			}
			return nil, fmt.Errorf("model candidate finished with reason: %v", candidate.FinishReason)
		}
		return nil, fmt.Errorf("no content returned in model candidate")
	}

	out := &Response{Provider: p.Name(), Model: p.model, Usage: geminiUsage(resp.UsageMetadata)}
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			slog.Info("LLM invoked function", "name", part.FunctionCall.Name)
			out.ToolCalls = append(out.ToolCalls, part.FunctionCall)
		} else if part.Text != "" {
			out.Text += part.Text
		}
	}
	if len(out.ToolCalls) > 0 {
		out.Text = ""
	}
	return out, nil
}
