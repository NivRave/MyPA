package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// stickyHarness records which tools were offered on each LLM call and replays scripted responses.
type stickyHarness struct {
	engine    *Engine
	mr        *miniredis.Miniredis
	mu        sync.Mutex
	offered   []map[string]bool // one entry per LLM Chat call
	responses []*llm.Response
}

func newStickyHarness(t *testing.T) *stickyHarness {
	t.Helper()
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	noop := func(context.Context, CallContext, string, map[string]interface{}) (string, error) { return "ok", nil }
	reg.Register(&mockToolModule{name: "workspace_gmail", executeFunc: noop, declarations: []*genai.FunctionDeclaration{
		{Name: "search_emails"}, {Name: "archive_emails"}, {Name: "soft_delete_emails"}}})
	reg.Register(&mockToolModule{name: "workspace_calendar", executeFunc: noop, declarations: []*genai.FunctionDeclaration{
		{Name: "create_calendar_event"}}})
	reg.Register(&mockToolModule{name: "baby", executeFunc: noop, declarations: []*genai.FunctionDeclaration{
		{Name: "log_baby_care"}}})
	engine.SetToolRegistry(reg)

	// Own miniredis so tests can fast-forward TTLs.
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	engine.store = store

	h := &stickyHarness{engine: engine, mr: mr}
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			names := map[string]bool{}
			if tools, ok := llm.ActiveToolsFromContext(ctx); ok {
				for _, d := range tools {
					names[d.Name] = true
				}
			}
			h.offered = append(h.offered, names)
			if len(h.responses) == 0 {
				return &llm.Response{Text: "ok"}, nil
			}
			r := h.responses[0]
			h.responses = h.responses[1:]
			return r, nil
		}).AnyTimes()
	return h
}

func (h *stickyHarness) turn(t *testing.T, text string, script ...*llm.Response) map[string]bool {
	t.Helper()
	h.mu.Lock()
	h.responses = script
	first := len(h.offered)
	h.mu.Unlock()
	require.NoError(t, h.engine.ProcessMessage(context.Background(), models.Message{
		UserID: "user-sticky", ChatID: "chat-sticky", Text: text, Source: "telegram",
	}))
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Greater(t, len(h.offered), first, "turn must call the LLM")
	return h.offered[first] // tools offered on the turn's first LLM call
}

// Acceptance: turn 1 "show my emails" → gmail; turn 2 follow-up without keywords → gmail tools present.
func TestStickyDomains_FollowUpKeepsPreviousDomain(t *testing.T) {
	h := newStickyHarness(t)

	t1 := h.turn(t, "show my emails")
	require.True(t, t1["search_emails"])

	t2 := h.turn(t, "yes, delete the second one")
	assert.True(t, t2["archive_emails"], "follow-up must still offer gmail tools")
	assert.True(t, t2["soft_delete_emails"])
}

// Domains of tools actually CALLED last turn are sticky too (even if the text didn't match them).
func TestStickyDomains_IncludesDomainsOfCalledTools(t *testing.T) {
	h := newStickyHarness(t)

	// Text matches baby; the model also calls a gmail tool.
	h.turn(t, "log the feeding and check if the pediatrician emailed",
		&llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "search_emails", Args: map[string]interface{}{}}}},
		&llm.Response{Text: "done"},
	)
	t2 := h.turn(t, "ok what did it say")
	assert.True(t, t2["search_emails"], "domain of a tool called last turn must be sticky")
	assert.True(t, t2["log_baby_care"], "domain matched by last turn's text must be sticky")
}

// Sticky domains decay: they carry over one turn, not forever.
func TestStickyDomains_DecayAfterAnUnrelatedTurn(t *testing.T) {
	h := newStickyHarness(t)

	h.turn(t, "show my emails")
	h.turn(t, "thanks!") // matches nothing, calls nothing → nothing to carry forward
	t3 := h.turn(t, "yes the second one")
	assert.False(t, t3["archive_emails"], "gmail must not stick around after an unrelated turn")
}

func TestStickyDomains_ExpireAfterTTL(t *testing.T) {
	h := newStickyHarness(t)
	h.turn(t, "show my emails")

	h.mr.FastForward(stickyDomainsTTL + time.Minute)
	t2 := h.turn(t, "yes the second one")
	assert.False(t, t2["archive_emails"])
}
