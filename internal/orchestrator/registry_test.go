package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

type mockToolModule struct {
	name         string
	declarations []*genai.FunctionDeclaration
	executeFunc  func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error)
	promptFunc   func(ctx context.Context, callCtx CallContext) string
}

func (m *mockToolModule) Name() string {
	return m.name
}

func (m *mockToolModule) Declarations() []*genai.FunctionDeclaration {
	return m.declarations
}

func (m *mockToolModule) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, callCtx, toolName, args)
	}
	return "ok", nil
}

func (m *mockToolModule) SystemPromptSection(ctx context.Context, callCtx CallContext) string {
	if m.promptFunc != nil {
		return m.promptFunc(ctx, callCtx)
	}
	return ""
}

func TestRegistry_RegisterAndGetModule(t *testing.T) {
	reg := NewRegistry()

	modA := &mockToolModule{
		name: "domainA",
		declarations: []*genai.FunctionDeclaration{
			{Name: "tool_a1"},
			{Name: "tool_a2"},
		},
	}
	modB := &mockToolModule{
		name: "domainB",
		declarations: []*genai.FunctionDeclaration{
			{Name: "tool_b1"},
		},
	}

	reg.Register(modA)
	reg.Register(modB)

	// Retrieve by name
	foundA, ok := reg.GetModule("domainA")
	assert.True(t, ok)
	assert.Equal(t, "domainA", foundA.Name())

	_, ok = reg.GetModule("nonexistent")
	assert.False(t, ok)

	// AllDeclarations
	decls := reg.AllDeclarations()
	assert.Len(t, decls, 3)

	names := []string{decls[0].Name, decls[1].Name, decls[2].Name}
	assert.Contains(t, names, "tool_a1")
	assert.Contains(t, names, "tool_a2")
	assert.Contains(t, names, "tool_b1")
}

func TestRegistry_Execute(t *testing.T) {
	reg := NewRegistry()
	ctx := context.Background()
	callCtx := CallContext{
		User: &models.User{PlatformID: "user_test"},
	}

	mod := &mockToolModule{
		name: "calculator",
		declarations: []*genai.FunctionDeclaration{
			{Name: "add"},
			{Name: "fail"},
		},
		executeFunc: func(ctx context.Context, c CallContext, toolName string, args map[string]interface{}) (string, error) {
			if toolName == "add" {
				return "sum=42", nil
			}
			if toolName == "fail" {
				return "", errors.New("boom")
			}
			return "", errors.New("unknown")
		},
	}
	reg.Register(mod)

	// 1. Success execution
	res, handled, err := reg.Execute(ctx, callCtx, "add", map[string]interface{}{"x": 20, "y": 22})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, "sum=42", res)

	// 2. Unhandled tool
	res, handled, err = reg.Execute(ctx, callCtx, "unknown_tool", nil)
	assert.NoError(t, err)
	assert.False(t, handled)
	assert.Empty(t, res)

	// 3. Execution error
	_, handled, err = reg.Execute(ctx, callCtx, "fail", nil)
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, "boom", err.Error())
}

func TestRegistry_CombinedSystemPrompt(t *testing.T) {
	reg := NewRegistry()
	ctx := context.Background()
	callCtx := CallContext{}

	mod1 := &mockToolModule{
		name: "baby",
		promptFunc: func(ctx context.Context, callCtx CallContext) string {
			return "Baby section: Emma is 4 months old."
		},
	}
	mod2 := &mockToolModule{
		name: "empty_prompt",
		promptFunc: func(ctx context.Context, callCtx CallContext) string {
			return "   " // whitespace should be trimmed and ignored
		},
	}
	mod3 := &mockToolModule{
		name: "house",
		promptFunc: func(ctx context.Context, callCtx CallContext) string {
			return "House section: Lease expires in 45 days."
		},
	}

	reg.Register(mod1)
	reg.Register(mod2)
	reg.Register(mod3)

	prompt := reg.CombinedSystemPrompt(ctx, callCtx)
	expected := "Baby section: Emma is 4 months old.\n\nHouse section: Lease expires in 45 days."
	assert.Equal(t, expected, prompt)
}

func TestRegistry_Concurrency(t *testing.T) {
	reg := NewRegistry()
	ctx := context.Background()
	callCtx := CallContext{}

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			m := &mockToolModule{
				name: string(rune('A' + id)),
				declarations: []*genai.FunctionDeclaration{
					{Name: string(rune('a' + id))},
				},
			}
			reg.Register(m)
			_ = reg.AllDeclarations()
			_ = reg.CombinedSystemPrompt(ctx, callCtx)
			_, _, _ = reg.Execute(ctx, callCtx, string(rune('a'+id)), nil)
		}(i)
	}

	wg.Wait()
	assert.NotEmpty(t, reg.Modules())
}
