package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// Provider is one chat model API (V4-02). The three adapters are Gemini (gemini.go), Anthropic
// (anthropic.go) and OpenAI-compatible (openai.go), which serves OpenAI, Grok and a local Ollama.
//
// Tool declarations and the tool calls a model returns use genai's plain structs
// (FunctionDeclaration, Schema, FunctionCall) as the one neutral form: every module already
// declares its tools with them, and each adapter translates them into its own wire format.
type Provider interface {
	// Name is the provider's name as LLM_PROVIDER spells it (config.Providers).
	Name() string
	// Model is the chat model the provider was configured with.
	Model() string
	// Local reports whether the model runs on a machine the install controls (Ollama), so a turn
	// kept local (LLM_LOCAL_DOMAINS) may go to it.
	Local() bool
	// Chat sends one request. An error that means the provider cannot serve requests right now
	// (quota, outage, timeout, no connection) is an *UnavailableError, which lets the router
	// fail over; any other error, and a *ContentBlockedError, is final.
	Chat(ctx context.Context, req Request) (*Response, error)
}

// Request is one chat request in a provider-neutral form.
type Request struct {
	// System is the system prompt.
	System string
	// History is the conversation so far. A message with tool calls is followed by a "function"
	// message holding their results in the same order (orchestrator sanitizeHistory).
	History []models.ChatMessage
	// Message is the new user message, and PhotoData/PhotoMimeType an attachment sent with it.
	Message       string
	PhotoData     []byte
	PhotoMimeType string
	// Tools are the declarations the model may call; empty means the request carries no tools.
	Tools []*genai.FunctionDeclaration
}

// Response represents the output from the LLM, which could be either a text reply or tool calls.
type Response struct {
	Text      string
	ToolCalls []*genai.FunctionCall
	IsError   bool
	// Provider names the provider that answered, which after a failover is not the primary.
	Provider string
	// Model is the model that answered (the provider's configured one).
	Model string
	// Usage is what the call cost, as the provider reported it (V4-04).
	Usage Usage
}

// Usage is the tokens one model call used (V4-04). Each adapter maps its API's fields onto these
// two: everything the request sent (prompt, history, tools, cached input) is input, everything
// the model produced (reply, tool calls, a thinking model's thoughts) is output. A provider that
// reports nothing gives zero.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Total is input plus output.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// ContentBlockedError indicates that the provider blocked the prompt or the reply under its
// safety or content policy. It is final: the router never retries a blocked turn elsewhere.
type ContentBlockedError struct {
	BlockReason string
	Provider    string
}

func (e *ContentBlockedError) Error() string {
	if e.Provider == "" {
		return fmt.Sprintf("model blocked prompt: %s", e.BlockReason)
	}
	return fmt.Sprintf("%s blocked prompt: %s", e.Provider, e.BlockReason)
}

// UnavailableError means a provider could not serve the request now: a quota or rate limit
// (HTTP 429), an outage or overload (5xx, 529), a timeout (408) or no connection at all. The
// router tries the next provider on it (LLM_FALLBACK).
type UnavailableError struct {
	Provider string
	// Status is the HTTP status, 0 when no response arrived.
	Status int
	Err    error
}

func (e *UnavailableError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s unavailable: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("%s unavailable (HTTP %d): %v", e.Provider, e.Status, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// IsUnavailable reports whether err means the provider could not serve the request now.
func IsUnavailable(err error) bool {
	var u *UnavailableError
	return errors.As(err, &u)
}

// unavailableStatus reports whether an HTTP status means the provider is unavailable rather than
// the request being wrong: 408, 429, any 5xx and Anthropic's 529 (overloaded).
func unavailableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// statusError turns an HTTP error status into the error a Provider returns. The body is cut short:
// providers describe the error there, not the request, but it is still kept out of INFO logs.
func statusError(provider string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	err := fmt.Errorf("HTTP %d: %s", status, msg)
	if unavailableStatus(status) {
		return &UnavailableError{Provider: provider, Status: status, Err: err}
	}
	return fmt.Errorf("%s request failed: %w", provider, err)
}

// transportError turns an error from sending a request into the error a Provider returns. A
// cancelled or expired turn is returned as it is, so the router does not fail over a turn that
// is already over; anything else (refused connection, DNS, timeout) means unavailable.
func transportError(ctx context.Context, provider string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s request: %w", provider, ctx.Err())
	}
	return &UnavailableError{Provider: provider, Err: err}
}

type localOnlyKey struct{}

// WithLocalOnly marks a turn as kept local (LLM_LOCAL_DOMAINS): the router sends it only to the
// local provider and never fails over to a cloud one.
func WithLocalOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, localOnlyKey{}, true)
}

// IsLocalOnly reports whether the turn is kept local.
func IsLocalOnly(ctx context.Context) bool {
	v, _ := ctx.Value(localOnlyKey{}).(bool)
	return v
}
