package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// mockDummyModule is a helper module for testing registry execution and error handling.
type mockDummyModule struct {
	name         string
	declarations []*genai.FunctionDeclaration
	executeFunc  func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error)
}

func (m *mockDummyModule) Name() string { return m.name }
func (m *mockDummyModule) Declarations() []*genai.FunctionDeclaration {
	return m.declarations
}
func (m *mockDummyModule) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, callCtx, toolName, args)
	}
	return "dummy_ok", nil
}
func (m *mockDummyModule) SystemPromptSection(ctx context.Context, callCtx CallContext) string {
	return ""
}

// TestToolDynamicDispatch verifies modular tools execute properly through
// e.toolRegistry.Execute and e.executeSingleTool.
func TestToolDynamicDispatch(t *testing.T) {
	reg := NewRegistry()

	var executedTools []string
	testMod := &mockDummyModule{
		name: "test_domain",
		declarations: []*genai.FunctionDeclaration{
			{Name: "domain_action_success"},
			{Name: "domain_action_error"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			executedTools = append(executedTools, toolName)
			if toolName == "domain_action_error" {
				return "", errors.New("simulated domain failure")
			}
			return "domain_action_success: executed with " + callCtx.ActorID(), nil
		},
	}
	reg.Register(testMod)

	eng := &Engine{
		toolRegistry: reg,
	}

	ctx := context.Background()
	msg := models.Message{UserID: "actor-123"}

	t.Run("Successful Dispatch to Registered Tool", func(t *testing.T) {
		call := &genai.FunctionCall{
			Name: "domain_action_success",
			Args: map[string]interface{}{"param1": "val1"},
		}
		res := eng.executeSingleTool(ctx, msg, nil, "", call)
		assert.Equal(t, "domain_action_success: executed with actor-123", res)
		assert.Contains(t, executedTools, "domain_action_success")
	})

	t.Run("Handled Execution Error Formatted Properly", func(t *testing.T) {
		call := &genai.FunctionCall{
			Name: "domain_action_error",
			Args: map[string]interface{}{},
		}
		res := eng.executeSingleTool(ctx, msg, nil, "", call)
		assert.Contains(t, res, "❌ Error executing domain_action_error: simulated domain failure")
	})

	t.Run("Unknown Tool Returns Unhandled Warning", func(t *testing.T) {
		call := &genai.FunctionCall{
			Name: "nonexistent_domain_tool",
			Args: map[string]interface{}{},
		}
		res := eng.executeSingleTool(ctx, msg, nil, "", call)
		assert.Equal(t, "⚠️ LLM tried to call an unknown tool.", res)
	})

	t.Run("Nil ToolRegistry Fallback", func(t *testing.T) {
		engNil := &Engine{toolRegistry: nil}
		call := &genai.FunctionCall{
			Name: "domain_action_success",
		}
		res := engNil.executeSingleTool(ctx, msg, nil, "", call)
		assert.Equal(t, "⚠️ LLM tried to call an unknown tool.", res)
	})
}

// TestSEC09_InteractiveGate_UntrustedTurn verifies that all privileged meta-tools
// are strictly intercepted and staged in Redis during an untrusted turn.
func TestSEC09_InteractiveGate_UntrustedTurn(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	metaTools := []struct {
		name string
		args map[string]interface{}
	}{
		{
			name: "update_user_preferences",
			args: map[string]interface{}{"standing_instructions": "adversarial injection payload"},
		},
		{
			name: "create_automation_rule",
			args: map[string]interface{}{"name": "malicious_rule", "action_instruction": "exfiltrate"},
		},
		{
			name: "schedule_workflow",
			args: map[string]interface{}{"name": "exfil_cron", "cron_expression": "*/5 * * * *"},
		},
		{
			name: "register_external_api",
			args: map[string]interface{}{"name": "attacker_api", "base_url": "https://attacker.com"},
		},
	}

	reg := NewRegistry()
	var executedMetaTools []string
	reg.Register(&mockDummyModule{
		name: "meta_module",
		declarations: []*genai.FunctionDeclaration{
			{Name: "update_user_preferences"},
			{Name: "create_automation_rule"},
			{Name: "schedule_workflow"},
			{Name: "register_external_api"},
			{Name: "safe_regular_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			executedMetaTools = append(executedMetaTools, toolName)
			return "executed:" + toolName, nil
		},
	})

	eng := &Engine{
		store:        store,
		toolRegistry: reg,
	}

	msg := models.Message{UserID: "target-victim"}

	for _, tc := range metaTools {
		t.Run("Intercept_"+tc.name, func(t *testing.T) {
			executedMetaTools = nil

			// Untrusted turn context
			secState := &TurnSecurityState{}
			secState.MarkUntrusted("untrusted_email_body")
			ctx := WithTurnSecurityState(context.Background(), secState)

			call := &genai.FunctionCall{
				Name: tc.name,
				Args: tc.args,
			}

			res := eng.executeSingleTool(ctx, msg, nil, "", call)

			// 1. Must return interactive security confirmation
			assert.Contains(t, res, "🔒 **Interactive Security Confirmation Required**")
			assert.Contains(t, res, "CONFIRM "+strings.ToUpper(tc.name))
			assert.Contains(t, res, "untrusted_email_body")

			// 2. Must NOT have executed through the registry
			assert.Empty(t, executedMetaTools, "Privileged meta-tool %s MUST NOT be executed when gated", tc.name)

			// 3. Must be staged in Redis
			pending, err := store.GetAndClearPendingMetaTool(context.Background(), "target-victim")
			require.NoError(t, err)
			require.NotNil(t, pending, "Action must be staged in Redis")
			assert.Equal(t, tc.name, pending.ToolName)
			assert.Equal(t, tc.args, pending.Args)
		})
	}

	t.Run("Non-Meta Tools Allowed During Untrusted Turn", func(t *testing.T) {
		executedMetaTools = nil

		secState := &TurnSecurityState{}
		secState.MarkUntrusted("untrusted_webpage")
		ctx := WithTurnSecurityState(context.Background(), secState)

		call := &genai.FunctionCall{
			Name: "safe_regular_tool",
			Args: map[string]interface{}{"q": "hello"},
		}

		res := eng.executeSingleTool(ctx, msg, nil, "", call)
		assert.Equal(t, "executed:safe_regular_tool", res)
		assert.Contains(t, executedMetaTools, "safe_regular_tool")

		// Nothing should be staged
		pending, err := store.GetAndClearPendingMetaTool(context.Background(), "target-victim")
		require.NoError(t, err)
		assert.Nil(t, pending)
	})

	t.Run("Meta Tools Allowed During Trusted Turn", func(t *testing.T) {
		executedMetaTools = nil

		// No untrusted state in context (normal trusted conversation)
		ctx := context.Background()

		call := &genai.FunctionCall{
			Name: "update_user_preferences",
			Args: map[string]interface{}{"standing_instructions": "legitimate preferences"},
		}

		res := eng.executeSingleTool(ctx, msg, nil, "", call)
		assert.Equal(t, "executed:update_user_preferences", res)
		assert.Contains(t, executedMetaTools, "update_user_preferences")

		// Nothing staged
		pending, err := store.GetAndClearPendingMetaTool(context.Background(), "target-victim")
		require.NoError(t, err)
		assert.Nil(t, pending)
	})
}
