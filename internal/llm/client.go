package llm

import (
	"context"
	"errors"
	"sync"

	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// Client is the chat client the orchestrator uses (the LLMClient port). It chooses a turn's tools,
// builds a provider-neutral Request and sends it through its Router, which picks the provider
// (V4-02). Embeddings never go through the router: they are made by the Embedder (V4-03).
type Client struct {
	router     *Router
	extraTools []*genai.FunctionDeclaration
	toolsMu    sync.RWMutex
	// embedder makes the embeddings GenerateEmbedding returns. It is set separately (UseEmbedder)
	// because the embedding model is not the chat model (V4-03).
	embedder *Embedder
}

// UseEmbedder sets the embedder GenerateEmbedding uses and returns the client.
func (c *Client) UseEmbedder(e *Embedder) *Client {
	c.embedder = e
	return c
}

// NewClient builds a client that chats with Gemini only, without failover.
func NewClient(ctx context.Context, apiKey, model string, opts ...func(*genai.ClientConfig)) (*Client, error) {
	gemini, err := NewGeminiProvider(ctx, apiKey, model, opts...)
	if err != nil {
		return nil, err
	}
	router, err := NewRouter([]Provider{gemini}, nil)
	if err != nil {
		return nil, err
	}
	return NewClientWithRouter(router), nil
}

// NewClientWithRouter builds a client that sends its requests through router.
func NewClientWithRouter(router *Router) *Client {
	return &Client{router: router}
}

// Router is the client's router.
func (c *Client) Router() *Router { return c.router }

// SetExtraTools sets additional function declarations available to the LLM during Chat.
func (c *Client) SetExtraTools(tools []*genai.FunctionDeclaration) {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	c.extraTools = make([]*genai.FunctionDeclaration, len(tools))
	copy(c.extraTools, tools)
}

// AddFunctionDeclarations appends additional function declarations to the client's toolset.
func (c *Client) AddFunctionDeclarations(tools ...*genai.FunctionDeclaration) {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	c.extraTools = append(c.extraTools, tools...)
}

type activeToolsKey struct{}
type noToolsContextKey struct{}

// WithActiveTools stores the turn-scoped tool declarations in context.
func WithActiveTools(ctx context.Context, tools []*genai.FunctionDeclaration) context.Context {
	return context.WithValue(ctx, activeToolsKey{}, tools)
}

// ActiveToolsFromContext retrieves the turn-scoped tool declarations from context.
func ActiveToolsFromContext(ctx context.Context) ([]*genai.FunctionDeclaration, bool) {
	val := ctx.Value(activeToolsKey{})
	if val == nil {
		return nil, false
	}
	tools, ok := val.([]*genai.FunctionDeclaration)
	return tools, ok
}

// WithNoTools returns a context instructing the LLM client to omit tool schemas.
func WithNoTools(ctx context.Context) context.Context {
	return context.WithValue(ctx, noToolsContextKey{}, true)
}

// IsNoTools checks whether the context specifies that no tools should be passed.
func IsNoTools(ctx context.Context) bool {
	v, _ := ctx.Value(noToolsContextKey{}).(bool)
	return v
}

func isNoTools(ctx context.Context) bool {
	return IsNoTools(ctx)
}

// Chat sends the conversation history and the new message to the model, through the router.
func (c *Client) Chat(ctx context.Context, systemInstruction string, history []models.ChatMessage, newMessage string, photoData []byte, photoMimeType string) (*Response, error) {
	return c.router.Chat(ctx, Request{
		System:        systemInstruction,
		History:       history,
		Message:       newMessage,
		PhotoData:     photoData,
		PhotoMimeType: photoMimeType,
		Tools:         c.toolsFor(ctx),
	})
}

// toolsFor is the turn's tool declarations: none under WithNoTools, the turn's own under
// WithActiveTools, and otherwise the client's tools followed by the base tools, each name once.
func (c *Client) toolsFor(ctx context.Context) []*genai.FunctionDeclaration {
	if isNoTools(ctx) {
		return nil
	}
	if scoped, ok := ActiveToolsFromContext(ctx); ok && len(scoped) > 0 {
		return scoped
	}
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	if CalendarEventTool == nil {
		return nil
	}
	if len(c.extraTools) == 0 {
		return CalendarEventTool.FunctionDeclarations
	}
	seen := make(map[string]bool)
	combined := make([]*genai.FunctionDeclaration, 0, len(CalendarEventTool.FunctionDeclarations)+len(c.extraTools))
	for _, list := range [][]*genai.FunctionDeclaration{c.extraTools, CalendarEventTool.FunctionDeclarations} {
		for _, d := range list {
			if d != nil && d.Name != "" && !seen[d.Name] {
				seen[d.Name] = true
				combined = append(combined, d)
			}
		}
	}
	return combined
}

// GenerateEmbedding returns the embedding of text, made by the client's embedder (UseEmbedder)
// with the embedding model, never with the chat model (V4-03).
func (c *Client) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if c.embedder == nil {
		return nil, errors.New("no embedding model is configured")
	}
	return c.embedder.Embed(ctx, text)
}
