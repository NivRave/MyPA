package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// BL-29: the request built for the default model carries no Temperature, nor any other sampling
// parameter, with or without tools.
func TestGeminiRequest_CarriesNoSamplingParameter(t *testing.T) {
	for _, req := range []Request{
		{System: "s", Message: "m"},
		{System: "s", Message: "m", Tools: CalendarEventTool.FunctionDeclarations},
	} {
		cfg := generateConfig(req)
		assert.Nil(t, cfg.Temperature)
		assert.Nil(t, cfg.TopP)
		assert.Nil(t, cfg.TopK)
		assert.Nil(t, cfg.Seed)
		assert.Nil(t, cfg.PresencePenalty)
		assert.Nil(t, cfg.FrequencyPenalty)
	}
	assert.Empty(t, generateConfig(Request{System: "s"}).Tools, "no tools when the request has none")
}

// The HTTP adapters pair each call with the result in the same position, and leave out what the
// APIs would refuse.
func TestBuildTurns_PairsCallsWithResults(t *testing.T) {
	history := []models.ChatMessage{
		{Role: "user", Content: "add milk and bread"},
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: "add", Args: map[string]any{"item": "milk"}}, {Name: "add", Args: map[string]any{"item": "bread"}}}},
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: "add", Response: map[string]any{"result": "1"}}, {Name: "add", Response: map[string]any{"result": "2"}}}},
		// Two calls but one result: the calls are left out, and so is the result, which then
		// answers no call.
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: "orphan"}, {Name: "orphan"}}},
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: "stray"}}},
		{Role: "assistant", Content: "done"},
	}
	turns := buildTurns(Request{History: history, Message: "thanks"})
	require.Len(t, turns, 5)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, "assistant", turns[1].Role)
	require.Len(t, turns[1].Parts, 2)
	assert.Equal(t, "user", turns[2].Role)
	require.Len(t, turns[2].Parts, 2)
	for i := range turns[1].Parts {
		assert.Equal(t, turns[1].Parts[i].CallID, turns[2].Parts[i].ResultID, "result %d answers its call", i)
	}
	assert.NotEqual(t, turns[1].Parts[0].CallID, turns[1].Parts[1].CallID, "each call has its own ID")
	assert.Equal(t, "assistant", turns[3].Role, "the orphan and the stray are left out, so done follows the results")
	assert.Equal(t, "done", turns[3].Parts[0].Text)
	assert.Equal(t, "thanks", turns[4].Parts[0].Text, "the new message follows as a user turn")
	turns = buildTurns(Request{History: history[:3], Message: "continue"})
	last := turns[len(turns)-1]
	assert.Equal(t, "user", last.Role)
	require.Len(t, last.Parts, 3, "the results and the continuation are one user turn")
	assert.NotNil(t, last.Parts[0].Result)
	assert.Equal(t, "continue", last.Parts[2].Text)
}

func TestJSONSchema_LowerCasesTypesAndKeepsStructure(t *testing.T) {
	s := jsonSchema(&genai.Schema{Type: genai.TypeObject, Required: []string{"a"}, Properties: map[string]*genai.Schema{
		"a": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString, Enum: []string{"x", "y"}}},
		"n": {Type: genai.TypeInteger, Description: "a number"},
	}})
	assert.Equal(t, "object", s["type"])
	assert.Equal(t, []string{"a"}, s["required"])
	props := s["properties"].(map[string]any)
	assert.Equal(t, "array", props["a"].(map[string]any)["type"])
	assert.Equal(t, map[string]any{"type": "string", "enum": []string{"x", "y"}}, props["a"].(map[string]any)["items"])
	assert.Equal(t, map[string]any{"type": "integer", "description": "a number"}, props["n"])
	assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, toolSchema(&genai.FunctionDeclaration{Name: "no_params"}))
}

type stubProvider struct {
	name  string
	local bool
	err   error
	calls int
}

func (s *stubProvider) Name() string  { return s.name }
func (s *stubProvider) Model() string { return "m" }
func (s *stubProvider) Local() bool   { return s.local }
func (s *stubProvider) Chat(context.Context, Request) (*Response, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &Response{Text: "ok", Provider: s.name}, nil
}

func TestNewRouter_Guards(t *testing.T) {
	_, err := NewRouter(nil, nil)
	require.Error(t, err)
	_, err = NewRouter([]Provider{&stubProvider{name: "gemini"}}, &stubProvider{name: "anthropic"})
	require.Error(t, err, "a cloud provider cannot be the local one")
	r, err := NewRouter([]Provider{&stubProvider{name: "gemini"}}, &stubProvider{name: "ollama", local: true})
	require.NoError(t, err)
	assert.Equal(t, "ollama", r.LocalProvider().Name())
}

// A cancelled turn is not failed over: the turn is already over.
func TestRouter_DoesNotFailOverACancelledTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	primary := &stubProvider{name: "gemini", err: &UnavailableError{Provider: "gemini", Err: context.Canceled}}
	fallback := &stubProvider{name: "anthropic"}
	r, err := NewRouter([]Provider{primary, fallback}, nil)
	require.NoError(t, err)
	cancel()
	_, err = r.Chat(ctx, Request{})
	require.Error(t, err)
	assert.Zero(t, fallback.calls)
}

func TestTransportError_CancelledIsNotUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, IsUnavailable(transportError(ctx, "openai", errors.New("x"))))
	assert.True(t, IsUnavailable(transportError(context.Background(), "openai", errors.New("x"))))
}

func TestCapabilities_DeclaredForEveryProvider(t *testing.T) {
	for _, p := range config.Providers() {
		c := CapabilitiesOf(p)
		assert.NotEmpty(t, c.Wire, p)
		assert.NotEmpty(t, c.ToolCalls, p)
		assert.NotEmpty(t, c.Images, p)
		assert.NotEmpty(t, c.PDF, p)
	}
	assert.True(t, CapabilitiesOf(config.ProviderOllama).Local, "Ollama is the local provider")
}
