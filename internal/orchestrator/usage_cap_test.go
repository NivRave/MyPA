package orchestrator_test

import (
	"context"
	"fmt"
	"html"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// capturedSessions records the audit sessions the engine publishes.
type capturedSessions struct {
	mu       sync.Mutex
	sessions []models.AuditSession
}

func (c *capturedSessions) Publish(_ context.Context, v any) error {
	if s, ok := v.(models.AuditSession); ok {
		c.mu.Lock()
		c.sessions = append(c.sessions, s)
		c.mu.Unlock()
	}
	return nil
}

func (c *capturedSessions) all() []models.AuditSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]models.AuditSession(nil), c.sessions...)
}

type usageHarness struct {
	t        *testing.T
	client   *db.Client
	spy      *spyTelegramClient
	sessions *capturedSessions
	engine   *orchestrator.Engine
	now      time.Time
}

var usageMessageSeq atomic.Int64

// newUsageHarness builds an engine on a real database with the given model and caps, its clock
// at 12:00 on 21 November 2026 in Jerusalem, the install's zone.
func newUsageHarness(t *testing.T, model orchestrator.LLMClient, caps orchestrator.UsageCaps) *usageHarness {
	t.Helper()
	client := newTestDBClient(t)
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	h := &usageHarness{t: t, client: client, spy: &spyTelegramClient{}, sessions: &capturedSessions{},
		now: time.Date(2026, 11, 21, 12, 0, 0, 0, jerusalem)}
	h.engine = orchestrator.NewEngine(nil, store, client, model, adapterstest.Registry(h.spy, nil),
		nil, nil, nil, nil, nil, nil, h.sessions, "Asia/Jerusalem").
		WithUsage(client, caps).
		WithClock(func() time.Time { return h.now })
	return h
}

// used records an earlier call of the person (nil: outside any person's turn) at the time.
func (h *usageHarness) used(person *models.User, tokens int64, at time.Time) {
	h.t.Helper()
	u := &models.ModelUsage{TurnID: "earlier", Provider: "gemini", Model: "m", InputTokens: tokens, CreatedAt: at}
	if person != nil {
		u.PersonID = &person.ID
	}
	require.NoError(h.t, h.client.RecordModelUsage(context.Background(), u))
}

// says sends text from the member and returns what the engine sent back to them.
func (h *usageHarness) says(member models.User, text string) []string {
	h.t.Helper()
	before := len(sentTexts(h.spy))
	require.NoError(h.t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
		Channel:      "telegram",
		MessageID:    fmt.Sprintf("usage-msg-%d", usageMessageSeq.Add(1)),
		Sender:       member.PlatformID,
		Conversation: channel.Conversation{ID: "chat-" + member.PlatformID},
		Text:         text,
	}))
	h.engine.Wait()
	// The Telegram adapter sends HTML; the person reads it unescaped.
	var out []string
	for _, m := range sentTexts(h.spy)[before:] {
		out = append(out, html.UnescapeString(m))
	}
	return out
}

func (h *usageHarness) tokensToday(person *models.User) (int64, int64) {
	h.t.Helper()
	y, m, d := h.now.Date()
	p, i, err := h.client.TokensSince(context.Background(), person.ID, time.Date(y, m, d, 0, 0, 0, 0, h.now.Location()))
	require.NoError(h.t, err)
	return p, i
}

// V4-04 done-when: "a capped user gets a clear message and no model call is made". The model
// mock expects nothing, so any chat or embedding call fails the test on its own; the person's
// total does not move, and a command still works.
func TestUsageCap_CappedPersonGetsAClearMessageAndNoModelCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl) // no EXPECT: the model must not be called at all
	h := newUsageHarness(t, model, orchestrator.UsageCaps{PersonDaily: 1000, InstallDaily: 1_000_000})
	dana := householdtest.Member(t, h.client, "cap-dana", "Dana", "admin", "CapFamily", "telegram")
	h.used(&dana, 1000, h.now.Add(-time.Hour))

	sent := h.says(dana, "What is on my calendar tomorrow?")
	require.Len(t, sent, 1, "one message, and only one")
	assert.Contains(t, sent[0], "chat-cap-dana:")
	assert.Contains(t, sent[0], "You have used today's allowance of the assistant")
	assert.Contains(t, sent[0], "00:00 (Asia/Jerusalem)", "the person is told when it resets, on the install's clock")
	assert.Contains(t, sent[0], "/help", "and that commands still work")
	person, _ := h.tokensToday(&dana)
	assert.EqualValues(t, 1000, person, "no call was made, so nothing was recorded")

	// A command needs no model, so it still works for a capped person.
	sent = h.says(dana, "/help")
	require.Len(t, sent, 1)
	assert.NotContains(t, sent[0], "today's allowance")

	// The turn is recorded as refused in the audit log, with no tokens.
	sessions := h.sessions.all()
	require.NotEmpty(t, sessions)
	assert.Equal(t, 0, sessions[0].TotalTokens)
	assert.Contains(t, sessions[0].FinalResponse, "today's allowance")
}

// The install cap stops every person once the whole install has used it, with its own message;
// a person under their own cap is still stopped.
func TestUsageCap_InstallCapStopsEveryone(t *testing.T) {
	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl) // no EXPECT
	h := newUsageHarness(t, model, orchestrator.UsageCaps{PersonDaily: 10_000, InstallDaily: 5000})
	dana := householdtest.Member(t, h.client, "inst-dana", "Dana", "admin", "InstallFamily", "telegram")
	other := householdtest.Member(t, h.client, "inst-ruth", "Ruth", "admin", "OtherFamily", "telegram")
	h.used(&other, 4000, h.now.Add(-time.Hour))
	h.used(nil, 1000, h.now.Add(-time.Hour)) // a call outside a person's turn counts for the install

	sent := h.says(dana, "Hello")
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "The assistant has used today's allowance for the whole install")
	assert.NotContains(t, sent[0], "You have used", "Dana is under her own cap; the message says whose cap it is")
}

// Under the caps every model call of a turn is recorded under the person and the turn, with
// who answered; the turn's total goes on its audit session; and the call that crosses the cap is
// the last one: the next turn is refused. Yesterday's tokens do not count.
func TestUsageCap_UnderTheCapEachCallIsRecordedAndTheNextTurnIsRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl)
	model.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	gomock.InOrder(
		// First call: the model asks for the current time (a built-in tool).
		model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "get_current_time"}}, Provider: "gemini", Model: "gemini-test",
				Usage: llm.Usage{InputTokens: 400, OutputTokens: 20}}, nil),
		// Second call, after the tool result: the reply, answered by a fallback provider.
		model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&llm.Response{Text: "It is noon.", Provider: "anthropic", Model: "claude-test",
				Usage: llm.Usage{InputTokens: 500, OutputTokens: 30}}, nil),
	)
	h := newUsageHarness(t, model, orchestrator.UsageCaps{PersonDaily: 1000})
	noam := householdtest.Member(t, h.client, "rec-noam", "Noam", "family", "RecFamily", "telegram")
	// 23:30 yesterday in Jerusalem, at the cap: a new day starts from zero.
	h.used(&noam, 1000, time.Date(2026, 11, 20, 23, 30, 0, 0, h.now.Location()))

	sent := h.says(noam, "What time is it?")
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "It is noon.")

	var rows []models.ModelUsage
	require.NoError(t, h.client.DB.Where("person_id = ? AND turn_id <> 'earlier'", noam.ID).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2, "one row per model call")
	assert.Equal(t, rows[0].TurnID, rows[1].TurnID, "both calls belong to one turn")
	assert.NotEmpty(t, rows[0].TurnID)
	assert.Equal(t, []string{"gemini", "anthropic"}, []string{rows[0].Provider, rows[1].Provider})
	assert.Equal(t, []string{"gemini-test", "claude-test"}, []string{rows[0].Model, rows[1].Model})
	assert.EqualValues(t, 400, rows[0].InputTokens)
	assert.EqualValues(t, 30, rows[1].OutputTokens)

	sessions := h.sessions.all()
	require.Len(t, sessions, 1)
	assert.Equal(t, rows[0].TurnID, sessions[0].ID, "the turn ID is the audit session's")
	assert.Equal(t, 950, sessions[0].TotalTokens, "the audit session carries the turn's total")

	person, _ := h.tokensToday(&noam)
	assert.EqualValues(t, 950, person, "yesterday's 1000 are not today's")

	// 950 is under 1000, so one more turn may start; it crosses the cap, and the next is refused.
	model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "Sure.", Provider: "gemini", Usage: llm.Usage{InputTokens: 90, OutputTokens: 10}}, nil).Times(1)
	sent = h.says(noam, "Thanks, and tell me a joke")
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "Sure.")
	sent = h.says(noam, "Another one")
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "You have used today's allowance of the assistant")
}

// Without caps (both 0) nothing is refused, and usage is still recorded for the dashboard.
func TestUsageCap_NoCapsRecordsButNeverRefuses(t *testing.T) {
	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl)
	model.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "ok", Provider: "gemini", Usage: llm.Usage{InputTokens: 10, OutputTokens: 1}}, nil).Times(1)
	h := newUsageHarness(t, model, orchestrator.UsageCaps{})
	dana := householdtest.Member(t, h.client, "nocap-dana", "Dana", "admin", "NoCapFamily", "telegram")
	h.used(&dana, 10_000_000, h.now.Add(-time.Minute))

	sent := h.says(dana, "Hello")
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "ok")
	person, _ := h.tokensToday(&dana)
	assert.EqualValues(t, 10_000_011, person)
}
