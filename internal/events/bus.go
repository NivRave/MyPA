package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// MutationEvent represents a standardized data mutation emitted across domain packages.
// It powers audit logs, undo/rollback operations, cross-user partner notifications,
// prompt cache invalidation, and event-driven automations.
type MutationEvent struct {
	ID          string `json:"id"`
	Topic       string `json:"topic"`       // e.g., "baby", "house", "finance", "schedule", "memory"
	Action      string `json:"action"`      // e.g., "create", "update", "delete", "complete"
	EntityType  string `json:"entity_type"` // e.g., "BabyCareLog", "FinancialTransaction"
	EntityID    string `json:"entity_id"`
	ActorUserID string `json:"actor_user_id"`
	// HouseholdID is the tenant the mutation happened in (V4-30). Subscribers that act on the
	// event (mutation log, partner notifications, automations, prompt cache) key by it.
	HouseholdID string `json:"household_id"`
	// FamilyGroup is the household's legacy label, still written beside HouseholdID until V4-44.
	FamilyGroup string    `json:"family_group"`
	BeforeJSON  string    `json:"before_json"`
	AfterJSON   string    `json:"after_json"`
	Summary     string    `json:"summary"` // Human-readable summary for partner notification & undo log
	Timestamp   time.Time `json:"timestamp"`
}

// ErrBusClosed is returned when an event is published to a closed EventBus.
var ErrBusClosed = errors.New("eventbus: bus is closed")

// Handler defines the signature for an event subscriber callback.
type Handler func(ctx context.Context, event MutationEvent) error

// orderedHandler pairs a unique identifier with a subscriber callback,
// ensuring strict FIFO execution sequence.
type orderedHandler struct {
	id      string
	handler Handler
}

// asyncTask represents a unit of work dispatched to background workers.
type asyncTask struct {
	ctx     context.Context
	event   MutationEvent
	handler Handler
}

// EventBusConfig defines tuning options for the dual-lane EventBus.
type EventBusConfig struct {
	WorkerCount   int
	QueueCapacity int
	AsyncTimeout  time.Duration
}

// DefaultEventBusConfig returns the production configuration defaults.
func DefaultEventBusConfig() EventBusConfig {
	return EventBusConfig{
		WorkerCount:   4,
		QueueCapacity: 1024,
		AsyncTimeout:  30 * time.Second,
	}
}

// Option modifies EventBusConfig.
type Option func(*EventBusConfig)

// WithWorkerCount overrides the number of background workers.
func WithWorkerCount(workers int) Option {
	return func(cfg *EventBusConfig) {
		if workers > 0 {
			cfg.WorkerCount = workers
		}
	}
}

// WithQueueCapacity overrides the async buffered queue capacity.
func WithQueueCapacity(capacity int) Option {
	return func(cfg *EventBusConfig) {
		if capacity > 0 {
			cfg.QueueCapacity = capacity
		}
	}
}

// WithAsyncTimeout overrides the per-job timeout for async handlers.
func WithAsyncTimeout(timeout time.Duration) Option {
	return func(cfg *EventBusConfig) {
		if timeout > 0 {
			cfg.AsyncTimeout = timeout
		}
	}
}

// EventBus is an in-memory, thread-safe dual-lane publish-subscribe event bus.
// It executes sync handlers immediately (for durability and undo logs) and dispatches
// async handlers to a bounded worker pool (for partner notifications, automations, and webhooks).
type EventBus struct {
	mu sync.RWMutex

	// Sync Lane handlers (ordered slices for deterministic FIFO execution)
	syncGlobals []orderedHandler
	syncTopics  map[string][]orderedHandler

	// Async Lane handlers (ordered slices for deterministic FIFO queueing)
	asyncGlobals []orderedHandler
	asyncTopics  map[string][]orderedHandler

	// Worker pool infrastructure
	cfg          EventBusConfig
	asyncQueue   chan asyncTask
	wg           sync.WaitGroup
	closed       atomic.Bool
	closeMu      sync.RWMutex
	stopWorkers  chan struct{}
	droppedCount atomic.Uint64
}

// NewEventBus initializes a new thread-safe dual-lane EventBus and starts background workers.
func NewEventBus(opts ...Option) *EventBus {
	cfg := DefaultEventBusConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	bus := &EventBus{
		syncGlobals:  make([]orderedHandler, 0),
		syncTopics:   make(map[string][]orderedHandler),
		asyncGlobals: make([]orderedHandler, 0),
		asyncTopics:  make(map[string][]orderedHandler),
		cfg:          cfg,
		asyncQueue:   make(chan asyncTask, cfg.QueueCapacity),
		stopWorkers:  make(chan struct{}),
	}

	bus.startWorkers()
	return bus
}

// startWorkers spawns the bounded worker goroutines.
func (b *EventBus) startWorkers() {
	for i := 0; i < b.cfg.WorkerCount; i++ {
		b.wg.Add(1)
		go func(workerID int) {
			defer b.wg.Done()
			for {
				select {
				case <-b.stopWorkers:
					// Drain remaining tasks in queue
					for task := range b.asyncQueue {
						b.executeAsyncTask(workerID, task)
					}
					return
				case task, ok := <-b.asyncQueue:
					if !ok {
						return
					}
					b.executeAsyncTask(workerID, task)
				}
			}
		}(i)
	}
}

// executeAsyncTask executes an async task safely with panic recovery.
func (b *EventBus) executeAsyncTask(workerID int, task asyncTask) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("eventbus async worker recovered from panic",
				"worker_id", workerID,
				"panic", r,
				"event_id", task.event.ID,
				"topic", task.event.Topic,
				"stack", string(debug.Stack()),
			)
		}
	}()

	taskCtx, cancel := context.WithTimeout(task.ctx, b.cfg.AsyncTimeout)
	defer cancel()

	if err := task.handler(taskCtx, task.event); err != nil {
		slog.Warn("eventbus async handler returned error",
			"worker_id", workerID,
			"event_id", task.event.ID,
			"topic", task.event.Topic,
			"error", err,
		)
	}
}

func executeSyncHandler(ctx context.Context, h Handler, ev MutationEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("eventbus sync handler recovered from panic",
				"panic", r,
				"event_id", ev.ID,
				"topic", ev.Topic,
				"stack", string(debug.Stack()),
			)
			err = fmt.Errorf("sync handler panic: %v", r)
		}
	}()
	return h(ctx, ev)
}

// Subscribe registers a handler on the Sync Lane (100% backward compatible default).
func (b *EventBus) Subscribe(handler Handler) {
	b.SubscribeSync(handler)
}

// SubscribeTopic registers a topic handler on the Sync Lane (100% backward compatible default).
func (b *EventBus) SubscribeTopic(topic string, handler Handler) {
	b.SubscribeSyncTopic(topic, handler)
}

// SubscribeSync registers a global handler that executes synchronously within Publish.
func (b *EventBus) SubscribeSync(handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.syncGlobals = append(b.syncGlobals, orderedHandler{
		id:      uuid.New().String(),
		handler: handler,
	})
}

// SubscribeSyncTopic registers a topic handler that executes synchronously within Publish.
func (b *EventBus) SubscribeSyncTopic(topic string, handler Handler) {
	if handler == nil || strings.TrimSpace(topic) == "" {
		return
	}
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.syncTopics[normTopic] = append(b.syncTopics[normTopic], orderedHandler{
		id:      uuid.New().String(),
		handler: handler,
	})
}

// SubscribeTopicSync is an alias for SubscribeSyncTopic.
func (b *EventBus) SubscribeTopicSync(topic string, handler Handler) {
	b.SubscribeSyncTopic(topic, handler)
}

// SubscribeAsync registers a global handler that executes asynchronously via the worker pool.
func (b *EventBus) SubscribeAsync(handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asyncGlobals = append(b.asyncGlobals, orderedHandler{
		id:      uuid.New().String(),
		handler: handler,
	})
}

// SubscribeAsyncTopic registers a topic handler that executes asynchronously via the worker pool.
func (b *EventBus) SubscribeAsyncTopic(topic string, handler Handler) {
	if handler == nil || strings.TrimSpace(topic) == "" {
		return
	}
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asyncTopics[normTopic] = append(b.asyncTopics[normTopic], orderedHandler{
		id:      uuid.New().String(),
		handler: handler,
	})
}

// SubscribeTopicAsync is an alias for SubscribeAsyncTopic.
func (b *EventBus) SubscribeTopicAsync(topic string, handler Handler) {
	b.SubscribeAsyncTopic(topic, handler)
}

// Publish dispatches an event across both Sync and Async lanes.
func (b *EventBus) Publish(ctx context.Context, event MutationEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed.Load() {
		return ErrBusClosed
	}

	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	normTopic := strings.ToLower(strings.TrimSpace(event.Topic))

	// Take handler snapshots under read lock in deterministic registration order
	b.mu.RLock()
	syncG := make([]Handler, len(b.syncGlobals))
	for i, oh := range b.syncGlobals {
		syncG[i] = oh.handler
	}

	var syncT []Handler
	if topicHandlers, ok := b.syncTopics[normTopic]; ok && len(topicHandlers) > 0 {
		syncT = make([]Handler, len(topicHandlers))
		for i, oh := range topicHandlers {
			syncT[i] = oh.handler
		}
	}

	asyncG := make([]Handler, len(b.asyncGlobals))
	for i, oh := range b.asyncGlobals {
		asyncG[i] = oh.handler
	}

	var asyncT []Handler
	if topicHandlers, ok := b.asyncTopics[normTopic]; ok && len(topicHandlers) > 0 {
		asyncT = make([]Handler, len(topicHandlers))
		for i, oh := range topicHandlers {
			asyncT[i] = oh.handler
		}
	}
	b.mu.RUnlock()

	// 1. Run Synchronous Lane (Durability / Mutation Log)
	var errs []error
	for _, h := range syncG {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := executeSyncHandler(ctx, h, event); err != nil {
			errs = append(errs, err)
		}
	}

	for _, h := range syncT {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := executeSyncHandler(ctx, h, event); err != nil {
			errs = append(errs, err)
		}
	}

	// 2. Enqueue to Asynchronous Lane (Worker Pool)
	if len(asyncG) > 0 || len(asyncT) > 0 {
		b.closeMu.RLock()
		if b.closed.Load() {
			b.closeMu.RUnlock()
			errs = append(errs, ErrBusClosed)
			return errors.Join(errs...)
		}

		detachedCtx := context.WithoutCancel(ctx)
		allAsync := append(asyncG, asyncT...)

		for _, h := range allAsync {
			task := asyncTask{
				ctx:     detachedCtx,
				event:   event,
				handler: h,
			}

			select {
			case b.asyncQueue <- task:
			default:
				b.droppedCount.Add(1)
				slog.Error("eventbus async queue full; dropped event task",
					"event_id", event.ID,
					"topic", event.Topic,
					"dropped_total", b.droppedCount.Load(),
				)
			}
		}
		b.closeMu.RUnlock()
	}

	return errors.Join(errs...)
}

// DroppedCount returns the number of async tasks dropped due to full queue buffer.
func (b *EventBus) DroppedCount() uint64 {
	return b.droppedCount.Load()
}

// ClearSubscribers clears all registered handlers (useful in unit tests).
func (b *EventBus) ClearSubscribers() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.syncGlobals = nil
	b.syncTopics = make(map[string][]orderedHandler)
	b.asyncGlobals = nil
	b.asyncTopics = make(map[string][]orderedHandler)
}

// Shutdown gracefully shuts down the worker pool, draining all pending tasks in the queue.
func (b *EventBus) Shutdown(ctx context.Context) error {
	return b.Close(ctx)
}

// Close gracefully shuts down the worker pool, draining all pending tasks in the queue.
// It can be called with an optional timeout context or with no parameters.
func (b *EventBus) Close(ctxs ...context.Context) error {
	if b.closed.Swap(true) {
		return nil // already closed
	}

	var ctx context.Context
	if len(ctxs) > 0 && ctxs[0] != nil {
		ctx = ctxs[0]
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}

	b.closeMu.Lock()
	close(b.stopWorkers)
	close(b.asyncQueue)
	b.closeMu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("eventbus shutdown timed out before workers drained: %w", ctx.Err())
	}
}
