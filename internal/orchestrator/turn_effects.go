package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
)

// turnEffects records whether the current turn produced external side effects. Once it has,
// a failure must not be retried (re-running the turn would duplicate those effects).
// Safe for concurrent use: tools execute in parallel goroutines.
type turnEffects struct {
	happened atomic.Bool

	mu    sync.Mutex
	tools []toolRecord // tools executed this turn (sticky domains P2-2, history/audit P2-9)
}

// toolRecord is one executed tool call with its (untruncated) arguments and result.
type toolRecord struct {
	name   string
	args   map[string]interface{}
	result string
}

const (
	// maxToolResultChars bounds each tool result kept in chat history (P2-9): enough for IDs,
	// titles and links ("delete that event"), small enough to keep 30 messages cheap.
	maxToolResultChars = 400
	// maxToolArgChars bounds each string argument kept in history.
	maxToolArgChars = 200
	// maxToolSummaryChars bounds the per-turn summary stored on audit_sessions.tool_summary.
	maxToolSummaryChars = 1000
)

// recordToolCall notes that a tool ran in the turn carried by ctx.
func recordToolCall(ctx context.Context, name string, args map[string]interface{}, result string) {
	if fx, ok := ctx.Value(turnEffectsKey{}).(*turnEffects); ok && fx != nil {
		fx.mu.Lock()
		fx.tools = append(fx.tools, toolRecord{name: name, args: args, result: result})
		fx.mu.Unlock()
	}
}

func (fx *turnEffects) records() []toolRecord {
	if fx == nil {
		return nil
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]toolRecord(nil), fx.tools...)
}

// calledTools returns the names of the tools executed this turn.
func (fx *turnEffects) calledTools() []string {
	var names []string
	for _, r := range fx.records() {
		names = append(names, r.name)
	}
	return names
}

// historyMessages returns the turn's tool activity as a Gemini-compatible pair — an assistant
// message with the calls immediately followed by a function message with the (truncated)
// results — or nil when no state-relevant tool ran. Read-only built-ins are omitted.
func (fx *turnEffects) historyMessages() []models.ChatMessage {
	var calls []models.FunctionCall
	var responses []models.FunctionResponse
	for _, r := range fx.records() {
		if readOnlyBuiltinTools[r.name] {
			continue
		}
		calls = append(calls, models.FunctionCall{Name: r.name, Args: compactArgs(r.args)})
		responses = append(responses, models.FunctionResponse{
			Name:     r.name,
			Response: map[string]interface{}{"result": truncateRunes(r.result, maxToolResultChars)},
		})
	}
	if len(calls) == 0 {
		return nil
	}
	return []models.ChatMessage{
		{Role: "assistant", ToolCalls: calls},
		{Role: "function", ToolResponses: responses},
	}
}

// toolSummary is a one-line, bounded description of the turn's tool activity for the audit log.
func (fx *turnEffects) toolSummary() string {
	var parts []string
	for _, r := range fx.records() {
		if readOnlyBuiltinTools[r.name] {
			continue
		}
		parts = append(parts, r.name+": "+truncateRunes(oneLine(r.result), 160))
	}
	return truncateRunes(strings.Join(parts, " | "), maxToolSummaryChars)
}

func compactArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			out[k] = truncateRunes(s, maxToolArgChars)
			continue
		}
		out[k] = v
	}
	return out
}

// truncateRunes shortens s to at most n runes (plus an ellipsis), never splitting a UTF-8
// sequence (Hebrew text is multi-byte).
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

type turnEffectsKey struct{}

func withTurnEffects(ctx context.Context, fx *turnEffects) context.Context {
	return context.WithValue(ctx, turnEffectsKey{}, fx)
}

// markSideEffect flags the turn in ctx as having produced an external side effect.
// What counts: a registry tool executed (even if it errored — it may have partially applied),
// an admin command acted (/backup, /undo), or the final reply was delivered.
// Progress notices ("Transcribed: …") deliberately do NOT count: repeating them is harmless,
// and treating them as side effects would turn every later failure into a lost message.
func markSideEffect(ctx context.Context) {
	if fx, ok := ctx.Value(turnEffectsKey{}).(*turnEffects); ok && fx != nil {
		fx.happened.Store(true)
	}
}

func (fx *turnEffects) any() bool { return fx != nil && fx.happened.Load() }

// readOnlyBuiltinTools are handled locally by the registry and never change external state,
// so executing them does not make a turn non-retryable.
var readOnlyBuiltinTools = map[string]bool{
	"get_current_time": true,
}
// turnUserKey carries the user resolved once at the start of a turn (P2-7), so tool calls don't
// each re-query the database for the same row.
type turnUserKey struct{}

func withTurnUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, turnUserKey{}, u)
}

func turnUser(ctx context.Context) *models.User {
	u, _ := ctx.Value(turnUserKey{}).(*models.User)
	return u
}

// inboundClaim is the dedup claim for one inbound message (zero value = no claim held).
type inboundClaim struct {
	store *state.Store
	key   string
}

// claimInbound deduplicates deliveries of the same inbound message. It returns duplicate=true
// when another delivery already claimed it. Messages without an ID cannot be deduplicated and
// are always processed. Redis errors fail open (process without dedup): the turn itself needs
// Redis for history, so it will fail and retry normally if Redis is truly down.
func (e *Engine) claimInbound(ctx context.Context, msg models.Message) (claim inboundClaim, duplicate bool) {
	if e.store == nil {
		return inboundClaim{}, false
	}
	if msg.ID == "" {
		slog.Warn("inbound message has no ID; idempotency dedup skipped", "user_id", msg.UserID, "source", msg.Source)
		return inboundClaim{}, false
	}
	key := state.InboundMessageKey(msg.Source, msg.ChatID, msg.UserID, msg.ID)
	ok, err := e.store.ClaimInbound(ctx, key, state.InboundDedupTTL)
	if err != nil {
		slog.Warn("inbound dedup claim failed; processing without dedup", "key", key, "error", err)
		return inboundClaim{}, false
	}
	if !ok {
		return inboundClaim{}, true
	}
	return inboundClaim{store: e.store, key: key}, false
}

// finish keeps the claim on success or non-retryable failure, and releases it on a retryable
// failure so the broker's redelivery is processed instead of being dropped as a duplicate.
func (c inboundClaim) finish(ctx context.Context, err error) {
	if c.store == nil || err == nil || errors.Is(err, broker.ErrNonRetryable) {
		return
	}
	// The turn context may already be cancelled/expired; release on a short detached context.
	relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if relErr := c.store.ReleaseInbound(relCtx, c.key); relErr != nil {
		slog.Warn("failed to release inbound dedup claim; retry will be dropped as duplicate", "key", c.key, "error", relErr)
	}
}

// shouldNotifyUserOfFailure: send the "internal error" notice only when no retry will follow —
// otherwise the user gets one error message per attempt for a request that may still succeed.
func shouldNotifyUserOfFailure(err error, info broker.DeliveryInfo) bool {
	return errors.Is(err, broker.ErrNonRetryable) || info.IsFinal()
}
