package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBus_GlobalAndTopicSubscribers(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx := context.Background()

	var (
		mu              sync.Mutex
		receivedGlobal  []MutationEvent
		receivedBaby    []MutationEvent
		receivedFinance []MutationEvent
	)

	bus.Subscribe(func(ctx context.Context, ev MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		receivedGlobal = append(receivedGlobal, ev)
		return nil
	})

	bus.SubscribeTopic("baby", func(ctx context.Context, ev MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		receivedBaby = append(receivedBaby, ev)
		return nil
	})

	bus.SubscribeTopic("Finance", func(ctx context.Context, ev MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		receivedFinance = append(receivedFinance, ev)
		return nil
	})

	// Publish baby event
	evBaby := MutationEvent{
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		EntityID:    "101",
		ActorUserID: "user_mom",
		Summary:     "Fed 120ml milk",
	}
	err := bus.Publish(ctx, evBaby)
	require.NoError(t, err)

	// Publish finance event (with mixed-case topic to test normalization)
	evFinance := MutationEvent{
		Topic:       "FINANCE",
		Action:      "create",
		EntityType:  "FinancialTransaction",
		EntityID:    "202",
		ActorUserID: "user_dad",
		Summary:     "Groceries 150 ILS",
	}
	err = bus.Publish(ctx, evFinance)
	require.NoError(t, err)

	// Publish house event (no topic subscriber)
	evHouse := MutationEvent{
		Topic:       "house",
		Action:      "update",
		EntityType:  "HomeMaintenanceItem",
		EntityID:    "303",
		ActorUserID: "user_dad",
		Summary:     "AC filter replaced",
	}
	err = bus.Publish(ctx, evHouse)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	// Global subscriber must have received all 3 events
	assert.Len(t, receivedGlobal, 3)
	// Auto-generated ID and non-zero timestamp
	for _, ev := range receivedGlobal {
		assert.NotEmpty(t, ev.ID)
		assert.False(t, ev.Timestamp.IsZero())
	}

	// Topic subscribers must have received exactly their topic's events
	assert.Len(t, receivedBaby, 1)
	assert.Equal(t, "101", receivedBaby[0].EntityID)

	assert.Len(t, receivedFinance, 1)
	assert.Equal(t, "202", receivedFinance[0].EntityID)
}

func TestEventBus_ErrorPropagation(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx := context.Background()

	err1 := errors.New("handler 1 failed")
	err2 := errors.New("handler 2 failed")

	var executedThirdHandler bool

	bus.Subscribe(func(ctx context.Context, ev MutationEvent) error {
		return err1
	})
	bus.SubscribeTopic("schedule", func(ctx context.Context, ev MutationEvent) error {
		return err2
	})
	bus.SubscribeTopic("schedule", func(ctx context.Context, ev MutationEvent) error {
		executedThirdHandler = true
		return nil
	})

	ev := MutationEvent{Topic: "schedule", Summary: "Event created"}
	err := bus.Publish(ctx, ev)

	// Publish returns joined error containing both errors
	require.Error(t, err)
	assert.ErrorIs(t, err, err1)
	assert.ErrorIs(t, err, err2)

	// Third handler must still have been executed despite prior errors
	assert.True(t, executedThirdHandler)
}

func TestEventBus_ContextCancellation(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before publish

	ev := MutationEvent{Topic: "test"}
	err := bus.Publish(ctx, ev)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestEventBus_Concurrency(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx := context.Background()

	const numSubscribers = 10
	const numPublishers = 10
	const eventsPerPublisher = 50

	var globalCount int64
	var topicCount int64

	for i := 0; i < numSubscribers; i++ {
		bus.Subscribe(func(ctx context.Context, ev MutationEvent) error {
			atomic.AddInt64(&globalCount, 1)
			return nil
		})
		bus.SubscribeTopic("concurrent", func(ctx context.Context, ev MutationEvent) error {
			atomic.AddInt64(&topicCount, 1)
			return nil
		})
	}

	var wg sync.WaitGroup
	wg.Add(numPublishers)

	for p := 0; p < numPublishers; p++ {
		go func(pubID int) {
			defer wg.Done()
			for e := 0; e < eventsPerPublisher; e++ {
				topic := "concurrent"
				if e%2 == 0 {
					topic = "other"
				}
				_ = bus.Publish(ctx, MutationEvent{
					Topic:   topic,
					Summary: fmt.Sprintf("pub %d event %d", pubID, e),
				})
			}
		}(p)
	}

	wg.Wait()

	totalPublished := numPublishers * eventsPerPublisher
	// Each event received by numSubscribers global handlers
	expectedGlobalCalls := int64(totalPublished * numSubscribers)
	assert.Equal(t, expectedGlobalCalls, atomic.LoadInt64(&globalCount))

	// Exactly half were topic "concurrent", each received by numSubscribers topic handlers
	expectedTopicCalls := int64((totalPublished / 2) * numSubscribers)
	assert.Equal(t, expectedTopicCalls, atomic.LoadInt64(&topicCount))
}

func TestEventBus_ClearSubscribers(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ctx := context.Background()

	var count int
	bus.Subscribe(func(ctx context.Context, ev MutationEvent) error {
		count++
		return nil
	})
	bus.SubscribeTopic("test", func(ctx context.Context, ev MutationEvent) error {
		count++
		return nil
	})

	_ = bus.Publish(ctx, MutationEvent{Topic: "test"})
	assert.Equal(t, 2, count)

	bus.ClearSubscribers()

	_ = bus.Publish(ctx, MutationEvent{Topic: "test"})
	assert.Equal(t, 2, count) // unchanged
}

func TestEventBus_DualLaneExecution(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(2), WithQueueCapacity(100))
	defer bus.Close()

	var syncDone atomic.Bool
	var asyncDone atomic.Bool

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		syncDone.Store(true)
		return nil
	})

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		asyncDone.Store(true)
		return nil
	})

	ctx := context.Background()
	err := bus.Publish(ctx, MutationEvent{Topic: "baby", Summary: "Baby feed"})
	require.NoError(t, err)

	// Sync handler must have completed BEFORE Publish returns
	assert.True(t, syncDone.Load(), "Sync handler must finish before Publish returns")

	// Async handler completes shortly after via worker pool
	assert.Eventually(t, func() bool {
		return asyncDone.Load()
	}, 2*time.Second, 10*time.Millisecond, "Async handler must finish on background worker")
}

func TestEventBus_AsyncWorkerPanicRecovery(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(2))
	defer bus.Close()

	var normalDone atomic.Bool

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		if ev.Summary == "panic" {
			panic("simulated fatal worker panic")
		}
		normalDone.Store(true)
		return nil
	})

	ctx := context.Background()
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "panic"})
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "normal"})

	assert.Eventually(t, func() bool {
		return normalDone.Load()
	}, 2*time.Second, 10*time.Millisecond, "Worker pool must recover from panic and process subsequent tasks")
}

func TestEventBus_SyncHandlerPanicRecovery(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	var normalDone atomic.Bool

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		panic("simulated sync handler panic")
	})

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		normalDone.Store(true)
		return nil
	})

	ctx := context.Background()
	err := bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "panic test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sync handler panic")
	assert.True(t, normalDone.Load(), "Subsequent sync handlers must still execute")
}

func TestEventBus_GracefulShutdownDraining(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(100))

	var processedCount atomic.Int64
	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		time.Sleep(10 * time.Millisecond)
		processedCount.Add(1)
		return nil
	})

	ctx := context.Background()
	const numEvents = 20
	for i := 0; i < numEvents; i++ {
		err := bus.Publish(ctx, MutationEvent{Topic: "drain", Summary: fmt.Sprintf("event-%d", i)})
		require.NoError(t, err)
	}

	// Graceful close drains all pending tasks
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := bus.Close(shutdownCtx)
	require.NoError(t, err)

	assert.Equal(t, int64(numEvents), processedCount.Load(), "All enqueued events must be drained before Close returns")
}

func TestEventBus_QueueCapacityAndDropCounter(t *testing.T) {
	// Worker count 1, queue capacity 2
	bus := NewEventBus(WithWorkerCount(1), WithQueueCapacity(2))
	defer bus.Close()

	workerBlocked := make(chan struct{}, 1)
	releaseWorker := make(chan struct{})

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		select {
		case workerBlocked <- struct{}{}:
		default:
		}
		<-releaseWorker
		return nil
	})

	ctx := context.Background()

	// 1st task: taken by worker, blocking it
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "task-1"})
	<-workerBlocked // Wait until worker starts task-1

	// Tasks 2 & 3: fill the queue of capacity 2
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "task-2"})
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "task-3"})

	// Tasks 4 & 5: queue is full, must be dropped without blocking
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "task-4"})
	_ = bus.Publish(ctx, MutationEvent{Topic: "test", Summary: "task-5"})

	assert.Equal(t, uint64(2), bus.DroppedCount(), "DroppedCount must equal 2")

	close(releaseWorker)
}

func TestEventBus_ContextDetachment(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(1))
	defer bus.Close()

	var receivedErr error
	var done atomic.Bool

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		time.Sleep(50 * time.Millisecond)
		receivedErr = ctx.Err()
		done.Store(true)
		return nil
	})

	// Caller context canceled immediately after publish
	callerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := bus.Publish(callerCtx, MutationEvent{Topic: "test", Summary: "detach"})
	require.NoError(t, err)

	time.Sleep(30 * time.Millisecond) // Ensure caller context expires
	assert.ErrorIs(t, callerCtx.Err(), context.DeadlineExceeded)

	assert.Eventually(t, func() bool {
		return done.Load()
	}, 2*time.Second, 10*time.Millisecond)

	assert.NoError(t, receivedErr, "Async worker context must be detached from caller cancellation")
}
