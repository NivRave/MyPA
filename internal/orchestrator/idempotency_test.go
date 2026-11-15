package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// setupIdempotencyEngine registers a single side-effecting tool and returns its execution counter.
func setupIdempotencyEngine(t *testing.T) (*Engine, *mocks.MockTelegramClient, *mocks.MockLLMClient, *int64) {
	t.Helper()
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	t.Cleanup(cleanup)

	var toolRuns int64
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "side_effects",
		declarations: []*genai.FunctionDeclaration{{Name: "create_calendar_event"}},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			atomic.AddInt64(&toolRuns, 1)
			return "event created", nil
		},
	})
	engine.SetToolRegistry(reg)

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return engine, tgMock, llmMock, &toolRuns
}

// expectToolTurn makes the LLM request the side-effecting tool, then answer with text.
func expectToolTurn(llmMock *mocks.MockLLMClient) {
	var calls int64
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			if atomic.AddInt64(&calls, 1) == 1 {
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "create_calendar_event", Args: map[string]interface{}{}}}}, nil
			}
			return &llm.Response{Text: "Done, event created."}, nil
		}).Times(2)
}

func tgMsg(id string) models.Message {
	return models.Message{ID: id, UserID: "user-idem", ChatID: "chat-idem", Text: "add dentist tomorrow 9am", Source: "telegram"}
}

// Acceptance: same message delivered twice → tools execute once.
func TestIdempotency_DuplicateDeliveryRunsToolsOnce(t *testing.T) {
	engine, tgMock, llmMock, toolRuns := setupIdempotencyEngine(t)
	expectToolTurn(llmMock) // exactly one LLM turn in total
	tgMock.EXPECT().SendMessage(gomock.Any(), "chat-idem", gomock.Any()).Return(nil).Times(1)

	msg := tgMsg("101")
	require.NoError(t, engine.ProcessMessage(context.Background(), msg))
	require.NoError(t, engine.ProcessMessage(context.Background(), msg), "duplicate must be acknowledged, not failed")

	assert.Equal(t, int64(1), atomic.LoadInt64(toolRuns))
}

// Acceptance: tool ran, then sending the reply failed → error is non-retryable (no requeue),
// and a redelivery is still treated as a duplicate.
func TestIdempotency_ToolRanThenSendFailed_NonRetryable(t *testing.T) {
	engine, tgMock, llmMock, toolRuns := setupIdempotencyEngine(t)
	expectToolTurn(llmMock)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("telegram 502")).AnyTimes()

	msg := tgMsg("102")
	err := engine.ProcessMessage(context.Background(), msg)
	require.Error(t, err)
	assert.ErrorIs(t, err, broker.ErrNonRetryable)

	require.NoError(t, engine.ProcessMessage(context.Background(), msg))
	assert.Equal(t, int64(1), atomic.LoadInt64(toolRuns), "redelivery must not re-run the tool")
}

// Acceptance: LLM call failed before any tool ran → error is retryable and the dedup claim is
// released, so the retry is actually processed.
func TestIdempotency_LLMFailedBeforeTools_Retryable(t *testing.T) {
	engine, tgMock, llmMock, toolRuns := setupIdempotencyEngine(t)

	gomock.InOrder(
		llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("gemini 503")).Times(1),
		llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&llm.Response{Text: "Hello again"}, nil).Times(1),
	)
	tgMock.EXPECT().SendMessage(gomock.Any(), "chat-idem", "Hello again").Return(nil).Times(1)

	msg := tgMsg("103")
	err := engine.ProcessMessage(context.Background(), msg)
	require.Error(t, err)
	assert.NotErrorIs(t, err, broker.ErrNonRetryable)

	require.NoError(t, engine.ProcessMessage(context.Background(), msg), "retry after a retryable failure must be processed")
	assert.Equal(t, int64(0), atomic.LoadInt64(toolRuns))
}

// Messages without an ID cannot be deduplicated; they must still be processed every time.
func TestIdempotency_EmptyMessageIDIsNotDeduplicated(t *testing.T) {
	engine, tgMock, llmMock, _ := setupIdempotencyEngine(t)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "hi"}, nil).Times(2)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)

	msg := tgMsg("")
	require.NoError(t, engine.ProcessMessage(context.Background(), msg))
	require.NoError(t, engine.ProcessMessage(context.Background(), msg))
}

// Same Telegram message_id in two different chats are different messages.
func TestIdempotency_KeyIsScopedPerChat(t *testing.T) {
	engine, tgMock, llmMock, _ := setupIdempotencyEngine(t)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "hi"}, nil).Times(2)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)

	a := tgMsg("7")
	b := tgMsg("7")
	b.ChatID = "other-chat"
	require.NoError(t, engine.ProcessMessage(context.Background(), a))
	require.NoError(t, engine.ProcessMessage(context.Background(), b))
}

// Read-only built-ins (get_current_time) are not side effects: a failure afterwards stays retryable.
func TestIdempotency_ReadOnlyBuiltinIsNotASideEffect(t *testing.T) {
	engine, tgMock, llmMock, _ := setupIdempotencyEngine(t)
	var calls int64
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			if atomic.AddInt64(&calls, 1) == 1 {
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "get_current_time", Args: map[string]interface{}{}}}}, nil
			}
			return &llm.Response{Text: "It is 9am."}, nil
		}).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("telegram 502")).AnyTimes()

	err := engine.ProcessMessage(context.Background(), tgMsg("104"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, broker.ErrNonRetryable, "only reading the clock must not block a retry")
}

func TestShouldNotifyUserOfFailure(t *testing.T) {
	retryable := errors.New("llm down")
	assert.False(t, shouldNotifyUserOfFailure(retryable, broker.DeliveryInfo{Attempt: 1, MaxAttempts: 3}),
		"intermediate retryable failures must stay silent")
	assert.True(t, shouldNotifyUserOfFailure(retryable, broker.DeliveryInfo{Attempt: 3, MaxAttempts: 3}),
		"final attempt must notify")
	assert.True(t, shouldNotifyUserOfFailure(broker.NonRetryable(retryable), broker.DeliveryInfo{Attempt: 1, MaxAttempts: 3}),
		"non-retryable failures will not be retried, so notify now")
}
