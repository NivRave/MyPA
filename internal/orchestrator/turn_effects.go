package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

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
	tools []string // names of tools executed this turn (for sticky domains, P2-2)
}

// recordToolCall notes that a tool ran in the turn carried by ctx.
func recordToolCall(ctx context.Context, name string) {
	if fx, ok := ctx.Value(turnEffectsKey{}).(*turnEffects); ok && fx != nil {
		fx.mu.Lock()
		fx.tools = append(fx.tools, name)
		fx.mu.Unlock()
	}
}

// calledTools returns a copy of the tool names executed this turn.
func (fx *turnEffects) calledTools() []string {
	if fx == nil {
		return nil
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]string(nil), fx.tools...)
}

type turnEffectsKey struct{}

func withTurnEffects(ctx context.Context, fx *turnEffects) context.Context {
	return context.WithValue(ctx, turnEffectsKey{}, fx)
}

// markSideEffect flags the turn in ctx as having produced an external side effect.
// What counts: a registry tool executed (even if it errored — it may have partially applied),
// an admin command acted (/backup, /restore, /undo), or the final reply was delivered.
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
