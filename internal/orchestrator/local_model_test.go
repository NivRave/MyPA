package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// localHarness records, per turn, whether its model calls were kept local and whether the turn's
// text was sent for an embedding.
type localHarness struct {
	engine *Engine
	mu     sync.Mutex
	local  []bool // one entry per Chat call
	embeds int
}

func newLocalHarness(t *testing.T, localDomains ...DomainGroup) *localHarness {
	t.Helper()
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	t.Cleanup(cleanup)
	reg := NewRegistry()
	noop := func(context.Context, CallContext, string, map[string]interface{}) (string, error) { return "ok", nil }
	reg.Register(&mockToolModule{name: "finance", executeFunc: noop, declarations: []*genai.FunctionDeclaration{{Name: "log_expense"}}})
	reg.Register(&mockToolModule{name: "workspace_gmail", executeFunc: noop, declarations: []*genai.FunctionDeclaration{{Name: "search_emails"}}})
	engine.SetToolRegistry(reg)
	engine.WithLocalDomains(localDomains)

	h := &localHarness{engine: engine}
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string) ([]float32, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.embeds++
		return []float32{0.1}, nil
	}).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.local = append(h.local, llm.IsLocalOnly(ctx))
			return &llm.Response{Text: "ok"}, nil
		}).AnyTimes()
	return h
}

// turn runs one message and returns whether each of its model calls was kept local, and how many
// embeddings it asked for.
func (h *localHarness) turn(t *testing.T, text string) ([]bool, int) {
	t.Helper()
	h.mu.Lock()
	first, embedsBefore := len(h.local), h.embeds
	h.mu.Unlock()
	require.NoError(t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
		Sender: "user-local", Conversation: channel.Conversation{ID: "chat-local"}, Text: text, Channel: "telegram",
	}))
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Greater(t, len(h.local), first, "the turn must call the model")
	return append([]bool(nil), h.local[first:]...), h.embeds - embedsBefore
}

// V4-02 local routing: a turn in a domain of LLM_LOCAL_DOMAINS is marked local for every model call
// (the router then sends it to Ollama only) and sends nothing for an embedding; a turn in another
// domain is not marked and keeps its memory search.
func TestLocalDomains_KeepTheTurnLocalAndSkipTheEmbedding(t *testing.T) {
	h := newLocalHarness(t, DomainFinance)

	local, embeds := h.turn(t, "how much did we spend on groceries this month? check the budget")
	for i, l := range local {
		assert.True(t, l, "model call %d of a finance turn must be kept local", i)
	}
	assert.Zero(t, embeds, "a local turn's text must not be sent to the embedding model")

	// The next turn is a follow-up: finance is sticky for one turn, so it stays local too.
	local, embeds = h.turn(t, "and last month?")
	assert.True(t, local[0], "a follow-up of a local turn stays local")
	assert.Zero(t, embeds)

	// Two turns later, an unrelated turn uses the cloud chain and searches memory again.
	h.turn(t, "show my emails from the school")
	local, embeds = h.turn(t, "show my emails from the school")
	assert.False(t, local[0], "a turn outside the local domains is not kept local")
	assert.Equal(t, 1, embeds, "and keeps its memory search")
}

// Without LLM_LOCAL_DOMAINS nothing changes: no turn is marked local.
func TestLocalDomains_NoneByDefault(t *testing.T) {
	h := newLocalHarness(t)
	local, embeds := h.turn(t, "how much did we spend on groceries this month? check the budget")
	assert.False(t, local[0])
	assert.Equal(t, 1, embeds)
}

func TestParseDomainGroups(t *testing.T) {
	got, err := ParseDomainGroups([]string{"finance", " Baby "})
	require.NoError(t, err)
	assert.Equal(t, []DomainGroup{DomainFinance, DomainBaby}, got)

	_, err = ParseDomainGroups([]string{"finances"})
	require.Error(t, err, "a typo must not silently send a local turn to the cloud")
	assert.Contains(t, err.Error(), `unknown domain "finances"`)
	assert.Contains(t, err.Error(), "finance", "the error lists the valid names")

	assert.Contains(t, DomainGroups(), DomainFinance)
	assert.Contains(t, DomainGroups(), DomainHistory)
}
