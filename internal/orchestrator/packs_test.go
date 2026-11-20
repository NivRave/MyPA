package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/packs"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/nivik/mypa/internal/toolcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// packTurn is what the model was given on a turn's first call.
type packTurn struct {
	systemPrompt string
	tools        []*genai.FunctionDeclaration
}

func (p packTurn) toolNames() map[string]bool {
	out := map[string]bool{}
	for _, d := range p.tools {
		out[d.Name] = true
	}
	return out
}

// toolsText is every word the model sees about its tools: names, descriptions and schemas.
func (p packTurn) toolsText(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(p.tools)
	require.NoError(t, err)
	return string(b)
}

// packHarness runs real turns through the engine, with every production module (the list the
// worker registers, toolcatalog.ProductionModules), a real database and a recording model.
type packHarness struct {
	t      *testing.T
	client *db.Client
	engine *orchestrator.Engine
	reg    *orchestrator.Registry
	user   models.User
	mu     sync.Mutex
	turns  []packTurn
}

func newPackHarness(t *testing.T) *packHarness {
	t.Helper()
	client := newTestDBClient(t)
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	h := &packHarness{t: t, client: client}
	ctrl := gomock.NewController(t)
	llmMock := mocks.NewMockLLMClient(ctrl)
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return(nil, errors.New("no embeddings in this test")).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, systemPrompt string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			tools, _ := llm.ActiveToolsFromContext(ctx)
			h.mu.Lock()
			h.turns = append(h.turns, packTurn{systemPrompt: systemPrompt, tools: tools})
			h.mu.Unlock()
			return &llm.Response{Text: "ok"}, nil
		}).AnyTimes()

	h.reg = orchestrator.NewRegistry()
	for _, m := range toolcatalog.ProductionModules() {
		h.reg.Register(m)
	}
	h.engine = orchestrator.NewEngine(nil, store, client, llmMock, adapterstest.Registry(&spyTelegramClient{}, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithToolRegistry(h.reg)
	h.user = householdtest.Member(t, client, "pack-parent", "Dana", "admin", "PackFamily", "telegram")
	return h
}

// setPacks stores the household's packs, as the dashboard's switch does.
func (h *packHarness) setPacks(set packs.Set) {
	h.t.Helper()
	_, err := h.client.SetHouseholdPacks(context.Background(), h.scope(), set)
	require.NoError(h.t, err)
}

func (h *packHarness) scope() identity.Scope {
	h.t.Helper()
	s, err := identity.ForUser(context.Background(), h.client, &h.user)
	require.NoError(h.t, err)
	return s
}

var packMessageSeq atomic.Int64

// turn sends text from the member and returns what the model was given on the turn's first call.
func (h *packHarness) turn(text string) packTurn {
	h.t.Helper()
	h.mu.Lock()
	first := len(h.turns)
	h.mu.Unlock()
	require.NoError(h.t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
		Channel:      "telegram",
		MessageID:    fmt.Sprintf("pack-msg-%d", packMessageSeq.Add(1)),
		Sender:       h.user.PlatformID,
		Conversation: channel.Conversation{ID: "chat-pack"},
		Text:         text,
	}))
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Greater(h.t, len(h.turns), first, "the turn must reach the model")
	return h.turns[first]
}

// A message that routes to every domain the matcher knows, so a pack's tools would be offered if
// anything let them through, plus the briefing the scheduler sends every morning.
var packMessages = []string{
	"The baby slept badly. Log a feeding of 120 ml, show my expenses and budget, check my emails, " +
		"fix the boiler, set up an automation rule with a webhook, and what are my preferences?",
	"Good morning! Please generate a brief summary of my schedule for today, summarize my pending TODO tasks, wish me a good day, and remind me of any important upcoming events.",
}

// V4-27 done-when: "a household with Baby disabled sees no baby tool, prompt text or page". The
// household holds baby data (kept while the pack is off), and the same turns with Baby on show
// the baby tools and the baby section, so the check cannot pass by accident. The morning briefing
// is a turn like any other, so it gets no baby lines either.
func TestPacks_BabyDisabledSeesNoBabyToolOrPromptText(t *testing.T) {
	h := newPackHarness(t)
	ctx := context.Background()
	require.NoError(t, h.client.UpsertBabyProfile(h.scope(), &models.BabyProfile{
		Name: "Noa", DateOfBirth: time.Now().AddDate(0, -3, 0), Gender: "female",
	}))

	h.setPacks(packs.Everything())
	on := h.turn(packMessages[0])
	assert.True(t, on.toolNames()["log_baby_care"], "with Baby on its tools are offered")
	assert.Contains(t, on.systemPrompt, "Noa", "with Baby on its section shows the baby")
	assert.Contains(t, strings.ToLower(on.systemPrompt), "baby")

	h.setPacks(packs.Everything().With(packs.Baby, false))
	for _, text := range packMessages {
		off := h.turn(text)
		assert.NotContains(t, strings.ToLower(off.systemPrompt), "baby", "no baby prompt text: %q", text)
		assert.NotContains(t, off.systemPrompt, "Noa", "the baby's data is kept, not shown")
		assert.NotContains(t, strings.ToLower(off.toolsText(t)), "baby", "no baby tool or tool text: %q", text)
		assert.True(t, off.toolNames()["save_topic_memory"], "Core stays")
		assert.True(t, off.toolNames()["log_financial_transaction"] || text != packMessages[0], "other packs stay on")
	}

	// A baby tool asked for anyway (a sticky follow-up, a hallucinated call) is refused.
	callCtx := orchestrator.CallContext{User: &h.user, Scope: h.scope(), DB: h.client}
	_, handled, err := h.reg.Execute(ctx, callCtx, "log_baby_care", map[string]interface{}{"log_type": "feeding"})
	assert.True(t, handled)
	require.ErrorIs(t, err, orchestrator.ErrPackOff)
	profiles, err := h.client.GetBabyProfilesForFamily(h.scope())
	require.NoError(t, err)
	assert.Len(t, profiles, 1, "switching the pack off keeps its data")
}

// V4-27 done-when: "a household with Automations disabled still has its preferences applied".
// Preferences and the personal CRM are Core: with Automations off their tools are still offered
// and the person's tone, quiet hours and standing instructions still reach the model, while the
// automation tools are gone.
func TestPacks_AutomationsDisabledStillAppliesPreferences(t *testing.T) {
	h := newPackHarness(t)
	require.NoError(t, h.client.UpsertUserPreference(&models.UserPreference{
		UserID: h.user.PlatformID, TonePersona: "Warm & Encouraging", PreferredLanguage: "Hebrew",
		QuietHoursStart: "22:00", QuietHoursEnd: "07:00", StandingInstructions: "Always answer in rhyme.",
		BriefingTopicsJSON: "[]", DefaultCurrency: "ILS",
	}))
	h.setPacks(packs.Everything().With(packs.Automations, false))

	got := h.turn(packMessages[0])
	for _, want := range []string{"Tone/Persona: Warm & Encouraging", "Preferred Language: Hebrew",
		"Quiet Hours: 22:00 to 07:00", "Always answer in rhyme."} {
		assert.Contains(t, got.systemPrompt, want)
	}
	names := got.toolNames()
	assert.True(t, names["update_user_preferences"], "preference tools are Core")
	assert.True(t, names["query_personal_crm"], "the personal CRM is Core")
	for _, tool := range []string{"register_external_api", "create_automation_rule", "schedule_workflow"} {
		assert.False(t, names[tool], "%s belongs to the Automations pack", tool)
	}
	assert.NotContains(t, got.systemPrompt, "### Automations & External APIs")

	// The morning briefing still applies them.
	brief := h.turn(packMessages[1])
	assert.Contains(t, brief.systemPrompt, "Always answer in rhyme.")
}

// DE-6: a household that switched every pack off is Core only: no tool of any pack is offered,
// whatever the message names, and Core is complete.
func TestPacks_CoreOnlyHouseholdGetsNoPackTools(t *testing.T) {
	h := newPackHarness(t)
	h.setPacks(packs.Set{})
	got := h.turn(packMessages[0])
	names := got.toolNames()
	for _, m := range toolcatalog.ProductionModules() {
		_, isPack := packs.OfModule(m.Name())
		for _, d := range m.Declarations() {
			if isPack {
				assert.False(t, names[d.Name], "%s (%s) is in a switched-off pack", d.Name, m.Name())
			}
		}
	}
	for _, core := range []string{"save_topic_memory", "schedule_reminder", "search_web", "undo_last_action", "update_user_preferences"} {
		assert.True(t, names[core], core)
	}
	assert.Contains(t, got.systemPrompt, "Topics in this household: family, health, personal, work.")
}
