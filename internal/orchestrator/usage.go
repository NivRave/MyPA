package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
)

// UsageMeter records the tokens of each chat model call and reads the totals the daily caps are
// checked against (V4-04). db.Client implements it.
type UsageMeter interface {
	RecordModelUsage(ctx context.Context, u *models.ModelUsage) error
	TokensSince(ctx context.Context, personID string, since time.Time) (person, install int64, err error)
}

// UsageCaps are the daily token caps (LLM_DAILY_TOKENS_PER_PERSON, LLM_DAILY_TOKENS_PER_INSTALL);
// 0 means no cap. A day is a calendar day on the install's clock (DEFAULT_TIMEZONE).
type UsageCaps struct {
	PersonDaily  int64
	InstallDaily int64
}

// WithUsage records every model call's tokens through the meter and refuses a turn when the
// person or the install has used its cap for the day. Without it (tests, tools) nothing is
// recorded and nothing is refused.
func (e *Engine) WithUsage(m UsageMeter, caps UsageCaps) *Engine {
	e.usage = m
	e.caps = caps
	return e
}

// model is the chat client the engine's turns and the tools call. It notes each call's tokens on
// the turn (its audit session's total) and, with a meter, records them.
func (e *Engine) model() LLMClient {
	if e.llm == nil {
		return nil // a nil interface, not a wrapper around one: callers check for nil
	}
	return meteredModel{LLMClient: e.llm, e: e}
}

// meteredModel is the engine's LLMClient with each chat call metered. Embeddings pass through:
// the embedding API reports no token count, and they are a small part of a turn's cost.
type meteredModel struct {
	LLMClient
	e *Engine
}

func (m meteredModel) Chat(ctx context.Context, systemPrompt string, history []models.ChatMessage, userMessage string, photoData []byte, photoMimeType string) (*llm.Response, error) {
	resp, err := m.LLMClient.Chat(ctx, systemPrompt, history, userMessage, photoData, photoMimeType)
	if err == nil && resp != nil {
		m.e.recordUsage(ctx, resp)
	}
	return resp, err
}

// recordUsage notes a call's tokens on the turn and records the call's row. The row is written
// even when the turn's context has just ended: the call was made and cost what it cost.
func (e *Engine) recordUsage(ctx context.Context, resp *llm.Response) {
	fx, _ := ctx.Value(turnEffectsKey{}).(*turnEffects)
	if fx != nil {
		fx.tokens.Add(int64(resp.Usage.Total()))
	}
	if e.usage == nil {
		return
	}
	row := &models.ModelUsage{Provider: resp.Provider, Model: resp.Model,
		InputTokens: int64(resp.Usage.InputTokens), OutputTokens: int64(resp.Usage.OutputTokens), CreatedAt: e.clock()}
	if fx != nil {
		row.TurnID = fx.turnID
	}
	if s, ok := turnScope(ctx); ok && s.PersonID() != "" {
		id := s.PersonID()
		row.PersonID = &id
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := e.usage.RecordModelUsage(wctx, row); err != nil {
		slog.Warn("model usage not recorded", "turn_id", row.TurnID, "provider", row.Provider, "error", err)
	}
}

// dayStart is the start of the install's calendar day at now.
func (e *Engine) dayStart() time.Time {
	loc := e.location(identity.Scope{})
	y, m, d := e.clock().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// usageRefusal is the message for a person who may not start a turn now because their cap or
// the install's is used up for the day; "" when the turn may run. The check is made before a
// turn, not during one: a turn that starts under the cap finishes, so a day's use can pass the
// cap by at most one turn. When the totals cannot be read the turn runs (the caps limit cost;
// they are not access control) and the failure is logged.
func (e *Engine) usageRefusal(ctx context.Context, scope identity.Scope) string {
	if e.usage == nil || (e.caps.PersonDaily <= 0 && e.caps.InstallDaily <= 0) {
		return ""
	}
	person, install, err := e.usage.TokensSince(ctx, scope.PersonID(), e.dayStart())
	if err != nil {
		slog.Warn("usage totals not read; the turn runs without the cap check", "person_id", scope.PersonID(), "error", err)
		return ""
	}
	resets := fmt.Sprintf("It starts again at 00:00 (%s). Commands such as /help and /undo still work.", e.location(identity.Scope{}).String())
	switch {
	case e.caps.PersonDaily > 0 && person >= e.caps.PersonDaily:
		slog.Info("turn refused: the person's daily usage cap is used", "person_id", scope.PersonID(), "tokens", person, "cap", e.caps.PersonDaily)
		return fmt.Sprintf("⏸️ You have used today's allowance of the assistant (%d of %d tokens). %s", person, e.caps.PersonDaily, resets)
	case e.caps.InstallDaily > 0 && install >= e.caps.InstallDaily:
		slog.Info("turn refused: the install's daily usage cap is used", "person_id", scope.PersonID(), "tokens", install, "cap", e.caps.InstallDaily)
		return "⏸️ The assistant has used today's allowance for the whole install, so it cannot answer until tomorrow. " + resets
	}
	return ""
}

// refuseCapped sends the refusal when the turn may not run and reports whether it did.
func (e *Engine) refuseCapped(ctx context.Context, scope identity.Scope, msg channel.Inbound) (string, bool, error) {
	text := e.usageRefusal(ctx, scope)
	if text == "" {
		return "", false, nil
	}
	return text, true, e.sendMessage(ctx, msg, text)
}

// turnTokens is the tokens of the turn so far.
func (fx *turnEffects) turnTokens() int {
	if fx == nil {
		return 0
	}
	return int(fx.tokens.Load())
}
