package orchestrator

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// validPairing reports whether history satisfies Gemini's constraints: starts with a plain user
// message, and every assistant tool-call message is immediately followed by its function-response
// message (and vice versa).
func validPairing(h []models.ChatMessage) bool {
	if len(h) == 0 {
		return true
	}
	if h[0].Role != "user" {
		return false
	}
	for i, m := range h {
		if len(m.ToolCalls) > 0 {
			if i+1 >= len(h) || h[i+1].Role != "function" || len(h[i+1].ToolResponses) != len(m.ToolCalls) {
				return false
			}
		}
		if m.Role == "function" {
			if i == 0 || len(h[i-1].ToolCalls) == 0 {
				return false
			}
		}
	}
	return true
}

func toolTurn(user, tool, result, reply string) []models.ChatMessage {
	return []models.ChatMessage{
		{Role: "user", Content: user},
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: tool, Args: map[string]interface{}{}}}},
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: tool, Response: map[string]interface{}{"result": result}}}},
		{Role: "assistant", Content: reply},
	}
}

// LTRIM can cut history anywhere; whatever survives must be sent to Gemini in a valid shape.
func TestSanitizeHistory_ValidAfterTrimAtEveryOffset(t *testing.T) {
	var full []models.ChatMessage
	full = append(full, toolTurn("add dentist", "create_calendar_event", "id=evt1", "Added")...)
	full = append(full, models.ChatMessage{Role: "user", Content: "thanks"}, models.ChatMessage{Role: "assistant", Content: "np"})
	full = append(full, toolTurn("delete it", "delete_calendar_event", "deleted evt1", "Deleted")...)

	for start := 0; start <= len(full); start++ {
		got := sanitizeHistory(full[start:])
		assert.True(t, validPairing(got), "trim at offset %d produced invalid history: %+v", start, got)
	}
	// Untrimmed history is kept intact.
	assert.Equal(t, full, sanitizeHistory(full))
}

func TestSanitizeHistory_DropsOrphans(t *testing.T) {
	h := []models.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: "x"}}}, // no response follows
		{Role: "assistant", Content: "hello"},
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: "y"}}}, // no call precedes
		{Role: "user", Content: "bye"},
	}
	got := sanitizeHistory(h)
	assert.True(t, validPairing(got))
	assert.Equal(t, []string{"hi", "hello", "bye"}, contents(got))
}

func contents(h []models.ChatMessage) []string {
	var out []string
	for _, m := range h {
		out = append(out, m.Content)
	}
	return out
}

// P2-9: a tool turn persists its (truncated) tool calls and results, so a later "delete that event"
// can see the event created earlier; the next turn's LLM call receives them.
func TestProcessMessage_PersistsToolContextInHistory(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	longResult := "Event Created Successfully! id=evt_42 " + strings.Repeat("x", 2000)
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "schedule",
		declarations: []*genai.FunctionDeclaration{{Name: "create_calendar_event"}},
		executeFunc: func(context.Context, CallContext, string, map[string]interface{}) (string, error) {
			return longResult, nil
		},
	})
	engine.SetToolRegistry(reg)

	var calls int64
	var secondTurnHistory []models.ChatMessage
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, history []models.ChatMessage, prompt string, _ []byte, _ string) (*llm.Response, error) {
			switch atomic.AddInt64(&calls, 1) {
			case 1:
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "create_calendar_event", Args: map[string]interface{}{"title": "Dentist"}}}}, nil
			case 2:
				return &llm.Response{Text: "Added the dentist."}, nil
			default:
				secondTurnHistory = history
				return &llm.Response{Text: "Deleted."}, nil
			}
		}).AnyTimes()

	ctx := context.Background()
	require.NoError(t, engine.ProcessMessage(ctx, channel.Inbound{MessageID: "h1", Sender: "u-hist", Conversation: channel.Conversation{ID: "c"}, Text: "add dentist tomorrow", Channel: "telegram"}))

	stored, err := engine.store.GetChatHistory(ctx, "u-hist")
	require.NoError(t, err)
	require.Len(t, stored, 4, "user, tool calls, tool results, reply")
	assert.Equal(t, "create_calendar_event", stored[1].ToolCalls[0].Name)
	assert.Equal(t, "Dentist", stored[1].ToolCalls[0].Args["title"])
	res := stored[2].ToolResponses[0].Response["result"].(string)
	assert.Contains(t, res, "id=evt_42")
	assert.LessOrEqual(t, len(res), maxToolResultChars+len("…"), "tool results are truncated in history")
	assert.Equal(t, "Added the dentist.", stored[3].Content)

	require.NoError(t, engine.ProcessMessage(ctx, channel.Inbound{MessageID: "h2", Sender: "u-hist", Conversation: channel.Conversation{ID: "c"}, Text: "delete that event", Channel: "telegram"}))
	require.True(t, validPairing(secondTurnHistory))
	var sawResult bool
	for _, m := range secondTurnHistory {
		for _, r := range m.ToolResponses {
			if strings.Contains(r.Response["result"].(string), "evt_42") {
				sawResult = true
			}
		}
	}
	assert.True(t, sawResult, "the next turn must see the earlier tool result")
}

func TestToolSummary_CompactAndBounded(t *testing.T) {
	fx := &turnEffects{}
	ctx := withTurnEffects(context.Background(), fx)
	recordToolCall(ctx, "create_calendar_event", map[string]interface{}{"title": "Dentist"}, "Event created id=evt_1")
	recordToolCall(ctx, "search_emails", nil, strings.Repeat("y", 5000))
	recordToolCall(ctx, "get_current_time", nil, "{...}") // read-only built-in: not part of the summary

	s := fx.toolSummary()
	assert.Contains(t, s, "create_calendar_event")
	assert.Contains(t, s, "evt_1")
	assert.Contains(t, s, "search_emails")
	assert.NotContains(t, s, "get_current_time")
	assert.LessOrEqual(t, len(s), maxToolSummaryChars+len("…"))
}

type capturePublisher struct {
	mu       sync.Mutex
	sessions []models.AuditSession
}

func (c *capturePublisher) Publish(_ context.Context, v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := v.(models.AuditSession); ok {
		c.sessions = append(c.sessions, s)
	}
	return nil
}

// P2-9: the audit session (Postgres, later the cold archive) records the turn's tool summary.
func TestProcessMessage_AuditSessionCarriesToolSummary(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()
	pub := &capturePublisher{}
	engine.publisher = pub
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "schedule",
		declarations: []*genai.FunctionDeclaration{{Name: "create_calendar_event"}},
		executeFunc: func(context.Context, CallContext, string, map[string]interface{}) (string, error) {
			return "Event created id=evt_77", nil
		},
	})
	engine.SetToolRegistry(reg)
	var calls int64
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string, []models.ChatMessage, string, []byte, string) (*llm.Response, error) {
			if atomic.AddInt64(&calls, 1) == 1 {
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "create_calendar_event", Args: map[string]interface{}{}}}}, nil
			}
			return &llm.Response{Text: "ok"}, nil
		}).AnyTimes()

	require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{MessageID: "s1", Sender: "u-sum", Conversation: channel.Conversation{ID: "c"}, Text: "add it", Channel: "telegram"}))
	engine.Wait() // telemetry is published asynchronously

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.sessions, 1)
	assert.Contains(t, pub.sessions[0].ToolSummary, "create_calendar_event")
	assert.Contains(t, pub.sessions[0].ToolSummary, "evt_77")
}
