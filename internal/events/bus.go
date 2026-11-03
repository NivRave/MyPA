package events

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MutationEvent represents a standardized data mutation emitted across domain packages.
// It powers audit logs, undo/rollback operations, cross-user partner notifications,
// and event-driven automations.
type MutationEvent struct {
	ID          string    `json:"id"`
	Topic       string    `json:"topic"`       // e.g., "baby", "house", "finance", "schedule", "memory"
	Action      string    `json:"action"`      // e.g., "create", "update", "delete", "complete"
	EntityType  string    `json:"entity_type"` // e.g., "BabyCareLog", "FinancialTransaction"
	EntityID    string    `json:"entity_id"`
	ActorUserID string    `json:"actor_user_id"`
	FamilyGroup string    `json:"family_group"`
	BeforeJSON  string    `json:"before_json"`
	AfterJSON   string    `json:"after_json"`
	Summary     string    `json:"summary"` // Human-readable summary for partner notification & undo log
	Timestamp   time.Time `json:"timestamp"`
}

// Handler defines the signature for an event subscriber callback.
type Handler func(ctx context.Context, event MutationEvent) error

// EventBus is an in-memory, thread-safe publish-subscribe event bus for MutationEvents.
type EventBus struct {
	mu               sync.RWMutex
	globalHandlers   []Handler
	topicHandlers    map[string][]Handler
}

// NewEventBus initializes a new thread-safe EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		globalHandlers: make([]Handler, 0),
		topicHandlers:  make(map[string][]Handler),
	}
}

// Subscribe registers a handler that receives all published MutationEvents.
func (b *EventBus) Subscribe(handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.globalHandlers = append(b.globalHandlers, handler)
}

// SubscribeTopic registers a handler that only receives MutationEvents matching the specified topic.
// Topic matching is normalized to lowercase for consistency.
func (b *EventBus) SubscribeTopic(topic string, handler Handler) {
	if handler == nil || strings.TrimSpace(topic) == "" {
		return
	}
	normTopic := strings.ToLower(strings.TrimSpace(topic))

	b.mu.Lock()
	defer b.mu.Unlock()
	b.topicHandlers[normTopic] = append(b.topicHandlers[normTopic], handler)
}

// Publish dispatches a MutationEvent to all global subscribers and matching topic subscribers.
// If event.ID is empty, a UUID is automatically generated.
// If event.Timestamp is zero, time.Now() is set.
// Handlers are executed safely; all handlers are invoked even if one returns an error,
// and errors are combined and returned via errors.Join.
func (b *EventBus) Publish(ctx context.Context, event MutationEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	normTopic := strings.ToLower(strings.TrimSpace(event.Topic))

	// Take snapshot of handlers under read lock to prevent holding lock during handler execution
	b.mu.RLock()
	globals := make([]Handler, len(b.globalHandlers))
	copy(globals, b.globalHandlers)

	var topicSubscribers []Handler
	if len(b.topicHandlers[normTopic]) > 0 {
		topicSubscribers = make([]Handler, len(b.topicHandlers[normTopic]))
		copy(topicSubscribers, b.topicHandlers[normTopic])
	}
	b.mu.RUnlock()

	var errs []error

	// Execute global handlers
	for _, h := range globals {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := h(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	// Execute topic handlers
	for _, h := range topicSubscribers {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := h(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// ClearSubscribers removes all registered global and topic handlers (useful for testing).
func (b *EventBus) ClearSubscribers() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.globalHandlers = make([]Handler, 0)
	b.topicHandlers = make(map[string][]Handler)
}
