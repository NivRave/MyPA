package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// ============================================================================
// STRESS SUITE 1: CONCURRENT TOOL EXECUTION (errgroup, 10-50 parallel calls)
// ============================================================================

// TestEmpirical_ExecuteToolsConcurrently_50ToolsOrderAlignment tests 50 parallel
// tool calls with varied completion times, errors, and panics.
// It verifies:
// 1. Strict 1:1 order alignment (funcCalls[i] and funcResponses[i] match toolCalls[i]).
// 2. No deadlocks under heavy parallel load.
// 3. Multi-tool panic recovery.
func TestEmpirical_ExecuteToolsConcurrently_50ToolsOrderAlignment(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := models.Message{UserID: "stress_user_50"}

	const totalTools = 50
	toolCalls := make([]*genai.FunctionCall, totalTools)
	expectedResults := make([]string, totalTools)
	isPanic := make([]bool, totalTools)

	// Configure mock module with 50 tools
	reg := NewRegistry()
	decls := make([]*genai.FunctionDeclaration, totalTools)
	for i := 0; i < totalTools; i++ {
		toolName := fmt.Sprintf("tool_stress_%02d", i)
		decls[i] = &genai.FunctionDeclaration{Name: toolName}
		toolCalls[i] = &genai.FunctionCall{
			Name: toolName,
			Args: map[string]interface{}{"idx": float64(i)},
		}

		// Tool 7, 21, 38, 49 will panic
		if i == 7 || i == 21 || i == 38 || i == 49 {
			isPanic[i] = true
			expectedResults[i] = fmt.Sprintf("panic in tool %d", i)
		} else {
			expectedResults[i] = fmt.Sprintf("success_%d", i)
		}
	}

	testMod := &mockToolModule{
		name:         "stress_tools_mod",
		declarations: decls,
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			idxFloat, _ := args["idx"].(float64)
			idx := int(idxFloat)

			// Random artificial delay between 5ms and 30ms to scramble execution order
			jitter := time.Duration(5+rand.Intn(25)) * time.Millisecond
			time.Sleep(jitter)

			if isPanic[idx] {
				panic(fmt.Sprintf("panic in tool %d", idx))
			}
			return fmt.Sprintf("success_%d", idx), nil
		},
	}
	reg.Register(testMod)
	engine.SetToolRegistry(reg)

	start := time.Now()
	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	elapsed := time.Since(start)

	// Assertions
	require.Len(t, funcCalls, totalTools, "funcCalls must match total input tools")
	require.Len(t, funcResponses, totalTools, "funcResponses must match total input tools")

	for i := 0; i < totalTools; i++ {
		expectedName := fmt.Sprintf("tool_stress_%02d", i)
		assert.Equal(t, expectedName, funcCalls[i].Name, "funcCalls[%d] name mismatch", i)
		assert.Equal(t, expectedName, funcResponses[i].Name, "funcResponses[%d] name mismatch", i)

		resStr, ok := funcResponses[i].Response["result"].(string)
		require.True(t, ok, "response result must be string at index %d", i)

		if isPanic[i] {
			assert.Contains(t, resStr, "Internal panic during tool execution", "index %d must contain panic recovery message", i)
			assert.Contains(t, resStr, fmt.Sprintf("panic in tool %d", i))
		} else {
			assert.Equal(t, fmt.Sprintf("success_%d", i), resStr, "index %d result mismatch", i)
		}
	}

	t.Logf("50 parallel tools completed in %v with strict order alignment", elapsed)
}

// TestEmpirical_ExecuteToolsConcurrently_SingleToolPanic tests single tool execution (n=1).
// Adversarial challenge: Does n=1 safely catch panics like n>1?
func TestEmpirical_ExecuteToolsConcurrently_SingleToolPanic(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := models.Message{UserID: "user_single_panic"}

	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name: "panic_single_mod",
		declarations: []*genai.FunctionDeclaration{
			{Name: "crash_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			panic("critical nil pointer exception in single tool")
		},
	})
	engine.SetToolRegistry(reg)

	toolCalls := []*genai.FunctionCall{
		{Name: "crash_tool"},
	}

	// We check if this call panics or recovers
	didPanic := false
	var funcCalls []models.FunctionCall
	var funcResponses []models.FunctionResponse

	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				t.Logf("Observed panic in n=1 execution: %v", r)
			}
		}()
		funcCalls, funcResponses = engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	}()

	if didPanic {
		t.Errorf("BUG DISCOVERED: executeToolsConcurrently with n=1 panics! It lacks panic recovery when n=1.")
	} else {
		require.Len(t, funcCalls, 1)
		require.Len(t, funcResponses, 1)
		resStr := funcResponses[0].Response["result"].(string)
		assert.Contains(t, resStr, "panic", "Single tool panic must be reported in response")
	}
}

// TestEmpirical_ExecuteToolsConcurrently_MaxLimitRespected asserts that errgroup.Group
// bounded limit (MaxConcurrentTools = 8) is strictly respected under heavy load.
func TestEmpirical_ExecuteToolsConcurrently_MaxLimitRespected(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := models.Message{UserID: "limit_user"}

	const total = 40
	toolCalls := make([]*genai.FunctionCall, total)
	decls := make([]*genai.FunctionDeclaration, total)

	var currentActive int64
	var maxActiveObserved int64

	for i := 0; i < total; i++ {
		name := fmt.Sprintf("limit_tool_%d", i)
		decls[i] = &genai.FunctionDeclaration{Name: name}
		toolCalls[i] = &genai.FunctionCall{Name: name}
	}

	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "limit_mod",
		declarations: decls,
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			active := atomic.AddInt64(&currentActive, 1)
			defer atomic.AddInt64(&currentActive, -1)

			// Track max active
			for {
				max := atomic.LoadInt64(&maxActiveObserved)
				if active <= max {
					break
				}
				if atomic.CompareAndSwapInt64(&maxActiveObserved, max, active) {
					break
				}
			}

			time.Sleep(15 * time.Millisecond)
			return "done", nil
		},
	})
	engine.SetToolRegistry(reg)

	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	require.Len(t, funcCalls, total)
	require.Len(t, funcResponses, total)

	maxObserved := atomic.LoadInt64(&maxActiveObserved)
	t.Logf("Max concurrent tools observed: %d (MaxConcurrentTools limit = %d)", maxObserved, MaxConcurrentTools)
	assert.LessOrEqual(t, maxObserved, int64(MaxConcurrentTools), "Active concurrent tool executions must never exceed MaxConcurrentTools (8)")
}

// TestEmpirical_ExecuteToolsConcurrently_ContextCancellationMidFlight tests context cancellation
// while 30 parallel tools are in progress.
func TestEmpirical_ExecuteToolsConcurrently_ContextCancellationMidFlight(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	msg := models.Message{UserID: "cancel_user"}

	const totalTools = 30
	toolCalls := make([]*genai.FunctionCall, totalTools)
	decls := make([]*genai.FunctionDeclaration, totalTools)

	var executedCount int64

	for i := 0; i < totalTools; i++ {
		name := fmt.Sprintf("slow_tool_%02d", i)
		decls[i] = &genai.FunctionDeclaration{Name: name}
		toolCalls[i] = &genai.FunctionCall{Name: name}
	}

	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "cancellation_mod",
		declarations: decls,
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			atomic.AddInt64(&executedCount, 1)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return "finished", nil
			}
		},
	})
	engine.SetToolRegistry(reg)

	start := time.Now()
	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	elapsed := time.Since(start)

	require.Len(t, funcCalls, totalTools)
	require.Len(t, funcResponses, totalTools)
	assert.Less(t, elapsed, 1000*time.Millisecond, "Must not block indefinitely on cancelled context")

	// Verify all returned responses are populated
	for i := 0; i < totalTools; i++ {
		assert.Equal(t, toolCalls[i].Name, funcResponses[i].Name)
		assert.NotNil(t, funcResponses[i].Response["result"])
	}
}

// TestEmpirical_ExecuteToolsConcurrently_PreCancelledContext tests pre-cancelled context.
func TestEmpirical_ExecuteToolsConcurrently_PreCancelledContext(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel

	msg := models.Message{UserID: "precancel_user"}
	toolCalls := []*genai.FunctionCall{
		{Name: "tool_a"},
		{Name: "tool_b"},
		{Name: "tool_c"},
	}

	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	require.Len(t, funcCalls, 3)
	require.Len(t, funcResponses, 3)
}

// ============================================================================
// STRESS SUITE 2: BOUNDED RECURSION DEPTH (handleToolCalls >= 8)
// ============================================================================

// TestEmpirical_BoundedRecursion_TerminatesAtDepth8 tests that even if the LLM
// persistently returns tool calls, the engine strictly stops after iteration 8
// and enforces WithNoTools(ctx).
func TestEmpirical_BoundedRecursion_TerminatesAtDepth8(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	llmMock := mocks.NewMockLLMClient(ctrl)
	tgMock := mocks.NewMockTelegramClient(ctrl)
	dbMock := mocks.NewMockDBClient(ctrl)
	eventPublisherMock := mocks.NewMockEventPublisher(ctrl)
	eventPublisherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	dbMock.EXPECT().GetUser(gomock.Any()).DoAndReturn(func(userID string) (*models.User, error) {
		return &models.User{PlatformID: userID}, nil
	}).AnyTimes()

	engine := NewEngine(
		nil, store, dbMock, llmMock, tgMock, nil, nil, nil, nil, nil, nil, nil, eventPublisherMock, "UTC",
	)

	reg := NewRegistry()
	toolCallCount := int64(0)
	reg.Register(&mockToolModule{
		name: "dummy_loop_mod",
		declarations: []*genai.FunctionDeclaration{
			{Name: "loop_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			atomic.AddInt64(&toolCallCount, 1)
			return fmt.Sprintf("executed tool step %d", atomic.LoadInt64(&toolCallCount)), nil
		},
	})
	engine.SetToolRegistry(reg)

	// Mock LLM behavior:
	// For iterations 1 through 7: LLM returns another tool call to force continuation.
	// For iteration 8 (the terminal summary prompt): LLM receives WithNoTools(ctx).
	var chatCallCount int64
	var sawNoToolsAtTermination bool

	llmMock.EXPECT().Chat(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).DoAndReturn(func(ctx context.Context, systemPrompt string, history []models.ChatMessage, prompt string, photoData []byte, photoMimeType string) (*llm.Response, error) {
		count := atomic.AddInt64(&chatCallCount, 1)

		if llm.IsNoTools(ctx) {
			sawNoToolsAtTermination = true
			return &llm.Response{Text: "Final summary: All 8 tasks completed successfully without further tools."}, nil
		}

		// If not terminal summary, keep demanding tool execution
		if count < 8 {
			return &llm.Response{
				ToolCalls: []*genai.FunctionCall{
					{Name: "loop_tool", Args: map[string]interface{}{"step": float64(count)}},
				},
			}, nil
		}

		// If for some reason LLM gets called with normal prompt on step 8+, also return tool calls
		return &llm.Response{
			ToolCalls: []*genai.FunctionCall{
				{Name: "loop_tool", Args: map[string]interface{}{"step": float64(count)}},
			},
		}, nil
	}).Times(8) // 7 continuation chats + 1 terminal summary chat

	msg := models.Message{UserID: "user_loop_test"}
	initialToolCalls := []*genai.FunctionCall{
		{Name: "loop_tool", Args: map[string]interface{}{"step": float64(0)}},
	}

	result, err := engine.handleToolCalls(context.Background(), msg, nil, "system prompt", initialToolCalls, 1)
	require.NoError(t, err)

	assert.True(t, sawNoToolsAtTermination, "Engine must invoke terminal summary with WithNoTools context")
	assert.Equal(t, "Final summary: All 8 tasks completed successfully without further tools.", result)
	assert.Equal(t, int64(8), atomic.LoadInt64(&toolCallCount), "Exactly 8 tool execution rounds must take place")
}

// TestEmpirical_BoundedRecursion_ImmediateHaltWhenDepthGTE8 tests calling handleToolCalls
// with iteration >= 8 directly.
func TestEmpirical_BoundedRecursion_ImmediateHaltWhenDepthGTE8(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	llmMock := mocks.NewMockLLMClient(ctrl)
	engine := &Engine{llm: llmMock}

	// Expect terminal summary call directly
	llmMock.EXPECT().Chat(
		gomock.Cond(func(x any) bool {
			c, ok := x.(context.Context)
			return ok && llm.IsNoTools(c)
		}),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).Return(&llm.Response{Text: "Terminated at step 8."}, nil).Times(1)

	msg := models.Message{UserID: "direct_halt_user"}
	toolCalls := []*genai.FunctionCall{{Name: "dummy"}}

	res, err := engine.handleToolCalls(context.Background(), msg, nil, "system", toolCalls, 8)
	require.NoError(t, err)
	assert.Equal(t, "Terminated at step 8.", res)

	// Test iteration 15 directly
	llmMock.EXPECT().Chat(
		gomock.Cond(func(x any) bool {
			c, ok := x.(context.Context)
			return ok && llm.IsNoTools(c)
		}),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).Return(&llm.Response{Text: "Terminated at step 15."}, nil).Times(1)

	res15, err := engine.handleToolCalls(context.Background(), msg, nil, "system", toolCalls, 15)
	require.NoError(t, err)
	assert.Equal(t, "Terminated at step 15.", res15)
}

// TestEmpirical_BoundedRecursion_StubbornLLMIgnoresNoTools tests what happens if an adversarial
// or misbehaving LLM returns ToolCalls during the terminal summary.
// Verification: The engine MUST NOT continue recursing.
func TestEmpirical_BoundedRecursion_StubbornLLMIgnoresNoTools(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	llmMock := mocks.NewMockLLMClient(ctrl)
	engine := &Engine{llm: llmMock}

	// Adversarial LLM returns empty text and returns a tool call even though WithNoTools was given!
	llmMock.EXPECT().Chat(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).Return(&llm.Response{
		Text: "",
		ToolCalls: []*genai.FunctionCall{
			{Name: "stubborn_tool"},
		},
	}, nil).Times(1)

	msg := models.Message{UserID: "stubborn_llm_user"}
	toolCalls := []*genai.FunctionCall{{Name: "dummy"}}

	res, err := engine.handleToolCalls(context.Background(), msg, nil, "system", toolCalls, 8)
	require.NoError(t, err)
	assert.Equal(t, "✅ All requested actions have been completed up to the maximum iteration limit.", res)
}

// ============================================================================
// STRESS SUITE 3: PROMPT CACHE CONCURRENCY & CONSISTENCY UNDER LOAD
// ============================================================================

// TestEmpirical_PromptCache_ThunderingHerd tests 100 simultaneous goroutines
// requesting CombinedSystemPrompt on a cold cache for the same familyGroup.
func TestEmpirical_PromptCache_ThunderingHerd(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)

	var modExecutions int64
	reg.Register(&mockToolModule{
		name: "thundering_mod",
		promptFunc: func(ctx context.Context, c CallContext) string {
			atomic.AddInt64(&modExecutions, 1)
			time.Sleep(10 * time.Millisecond) // Simulate DB query latency
			return "Thundering Section Content"
		},
	})

	const numCallers = 100
	var wg sync.WaitGroup
	wg.Add(numCallers)

	callCtx := CallContext{
		User: &models.User{FamilyGroup: "fam_herd_99"},
	}

	results := make([]string, numCallers)
	for i := 0; i < numCallers; i++ {
		idx := i
		go func() {
			defer wg.Done()
			results[idx] = reg.CombinedSystemPrompt(context.Background(), callCtx)
		}()
	}

	wg.Wait()

	// Verify all 100 callers got identical, valid non-empty responses
	for i := 0; i < numCallers; i++ {
		assert.Equal(t, "Thundering Section Content", results[i], "Caller %d received unexpected prompt", i)
	}

	// Verify Redis cache is valid
	val, found, err := store.GetPromptSection(context.Background(), "fam_herd_99", "thundering_mod")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "Thundering Section Content", val)

	t.Logf("Thundering herd: 100 concurrent callers completed with zero race or data inconsistency")
}

// TestEmpirical_PromptCache_SimultaneousMutationsAndReads stresses the prompt cache
// with 50 readers, 25 EventBus invalidators, and dynamic module output updates simultaneously.
func TestEmpirical_PromptCache_SimultaneousMutationsAndReads(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, store)

	reg := NewRegistry().WithPromptCache(store)

	var currentVersion int64
	reg.Register(&mockToolModule{
		name: "baby",
		promptFunc: func(ctx context.Context, c CallContext) string {
			ver := atomic.LoadInt64(&currentVersion)
			return fmt.Sprintf("Baby Section Ver %d", ver)
		},
	})
	reg.Register(&mockToolModule{
		name: "finance",
		promptFunc: func(ctx context.Context, c CallContext) string {
			ver := atomic.LoadInt64(&currentVersion)
			return fmt.Sprintf("Finance Section Ver %d", ver)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var readErrors int64
	var readSuccesses int64

	callCtx := CallContext{
		User: &models.User{FamilyGroup: "fam_dyn_stress"},
	}

	// 40 concurrent readers
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				prompt := reg.CombinedSystemPrompt(ctx, callCtx)
				if ctx.Err() != nil {
					break
				}
				if prompt == "" || !strings.Contains(prompt, "Section Ver") {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&readSuccesses, 1)
				}
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	// 20 concurrent invalidators
	for i := 0; i < 20; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			topics := []string{"baby", "finance"}
			for ctx.Err() == nil {
				atomic.AddInt64(&currentVersion, 1)
				topic := topics[workerID%len(topics)]
				_ = bus.Publish(ctx, events.MutationEvent{
					Topic:       topic,
					FamilyGroup: "fam_dyn_stress",
					Action:      "update",
				})
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(0), atomic.LoadInt64(&readErrors), "Zero read corruption errors allowed under concurrent mutation")
	assert.True(t, atomic.LoadInt64(&readSuccesses) > 100, "Should have executed hundreds of successful reads")
	t.Logf("Simultaneous mutations & reads: %d successful reads with 0 errors", atomic.LoadInt64(&readSuccesses))
}

// TestEmpirical_PromptCache_RedisFailureUnderConcurrency tests 50 concurrent callers
// when Redis throws errors or connection failures.
func TestEmpirical_PromptCache_RedisFailureUnderConcurrency(t *testing.T) {
	callCtx := CallContext{
		User: &models.User{FamilyGroup: "fam_fail_stress"},
	}

	failingCache := &mockFailingPromptCache{
		getErr: errors.New("simulated redis network partition"),
		setErr: errors.New("simulated redis disk full"),
	}

	reg := NewRegistry().WithPromptCache(failingCache)
	reg.Register(&mockToolModule{
		name: "resilient_mod",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Fallback Section Content"
		},
	})

	const callers = 50
	var wg sync.WaitGroup
	wg.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
			assert.Equal(t, "Fallback Section Content", prompt)
		}()
	}

	wg.Wait()
}
