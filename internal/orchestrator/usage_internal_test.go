package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingMeter struct {
	mu   sync.Mutex
	rows []models.ModelUsage
}

func (m *recordingMeter) RecordModelUsage(_ context.Context, u *models.ModelUsage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, *u)
	return nil
}

func (m *recordingMeter) TokensSince(context.Context, string, time.Time) (int64, int64, error) {
	return 0, 0, nil
}

type answeringModel struct{ resp *llm.Response }

func (a answeringModel) Chat(context.Context, string, []models.ChatMessage, string, []byte, string) (*llm.Response, error) {
	return a.resp, nil
}
func (a answeringModel) GenerateEmbedding(context.Context, string) ([]float32, error) {
	return nil, nil
}

// A tool that calls the model itself (web_research summarises a page) gets the metered client:
// its call is recorded under the turn and counts for the turn's total (V4-04).
func TestUsage_AToolsOwnModelCallIsMetered(t *testing.T) {
	meter := &recordingMeter{}
	e := &Engine{llm: answeringModel{&llm.Response{Text: "summary", Provider: "gemini", Usage: llm.Usage{InputTokens: 60, OutputTokens: 4}}}}
	e.WithUsage(meter, UsageCaps{})
	fx := &turnEffects{turnID: "turn-42"}
	ctx := withTurnEffects(context.Background(), fx)

	callCtx := e.buildCallContext(ctx, &models.User{PlatformID: "p"}, channel.Inbound{Sender: "p"})
	_, err := callCtx.LLM.Chat(ctx, "", nil, "summarise this", nil, "")
	require.NoError(t, err)

	require.Len(t, meter.rows, 1)
	assert.Equal(t, "turn-42", meter.rows[0].TurnID)
	assert.EqualValues(t, 60, meter.rows[0].InputTokens)
	assert.Equal(t, 64, fx.turnTokens())
}

// An engine without a model gives tools a nil client, not a wrapper around nil, so a module's
// "no model" check still holds.
func TestUsage_NoModelGivesToolsNil(t *testing.T) {
	e := &Engine{}
	assert.Nil(t, e.buildCallContext(context.Background(), nil, channel.Inbound{}).LLM)
}
