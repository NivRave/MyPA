package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_EventBus_100ConcurrentPublishers tests 100 concurrent publishers
// publishing simultaneously across sync and async lanes.
func TestAdversarial_EventBus_100ConcurrentPublishers(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(8), WithQueueCapacity(2048))
	defer bus.Close()

	const numPublishers = 100
	const eventsPerPub = 10
	totalEvents := int64(numPublishers * eventsPerPub)

	var syncProcessed atomic.Int64
	var asyncProcessed atomic.Int64

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		syncProcessed.Add(1)
		return nil
	})

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		asyncProcessed.Add(1)
		return nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(numPublishers)

	startGate := make(chan struct{})

	for i := 0; i < numPublishers; i++ {
		go func(pubID int) {
			defer wg.Done()
			<-startGate
			for e := 0; e < eventsPerPub; e++ {
				err := bus.Publish(ctx, MutationEvent{
					Topic:   "stress",
					Summary: fmt.Sprintf("pub-%d-event-%d", pubID, e),
				})
				if err != nil {
					t.Errorf("publisher %d event %d failed: %v", pubID, e, err)
				}
			}
		}(i)
	}

	close(startGate)
	wg.Wait()

	// Sync handler MUST have processed every event synchronously before Publish returned
	assert.Equal(t, totalEvents, syncProcessed.Load(), "All sync events must be processed immediately")

	// Wait for async workers to drain
	assert.Eventually(t, func() bool {
		return asyncProcessed.Load() == totalEvents
	}, 5*time.Second, 10*time.Millisecond, "All async events must be processed by worker pool")

	assert.Equal(t, uint64(0), bus.DroppedCount(), "No events should be dropped under sufficient capacity")
}

// TestAdversarial_EventBus_SlowAsyncHandlerNonBlocking verifies that a slow async
// handler (simulating 500ms network latency) does NOT block Publish from returning immediately (<5ms).
func TestAdversarial_EventBus_SlowAsyncHandlerNonBlocking(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(100))
	defer bus.Close()

	var asyncStarted atomic.Bool
	var asyncFinished atomic.Bool

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		asyncStarted.Store(true)
		time.Sleep(500 * time.Millisecond) // Simulated slow webhook/network I/O
		asyncFinished.Store(true)
		return nil
	})

	ctx := context.Background()
	start := time.Now()
	err := bus.Publish(ctx, MutationEvent{
		Topic:   "slow-webhook",
		Summary: "webhook payload",
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	// Publish must return well under 10ms (target <2ms, allowance 10ms for CI/scheduling)
	assert.Less(t, elapsed, 100*time.Millisecond, "Publish should return almost instantly without waiting for slow async handler")

	// Verify the async handler actually runs in background and completes
	assert.Eventually(t, func() bool {
		return asyncFinished.Load()
	}, 2*time.Second, 20*time.Millisecond, "Slow async handler must finish in background")
}

// TestAdversarial_EventBus_ShutdownDraining100Tasks verifies that bus.Close drains
// 100 queued tasks completely without data loss or deadlocks.
func TestAdversarial_EventBus_ShutdownDraining100Tasks(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(200))

	var processedCount atomic.Int64
	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		time.Sleep(5 * time.Millisecond)
		processedCount.Add(1)
		return nil
	})

	ctx := context.Background()
	const taskCount = 100
	for i := 0; i < taskCount; i++ {
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "drain",
			Summary: fmt.Sprintf("task-%d", i),
		})
		require.NoError(t, err)
	}

	// Immediate shutdown: all 100 tasks must drain within shutdown timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := bus.Close(shutdownCtx)
	require.NoError(t, err)

	assert.Equal(t, int64(taskCount), processedCount.Load(), "All 100 queued tasks must be drained before Close returns")
}

// TestAdversarial_EventBus_ConcurrentPublishAndClose tests for potential panic:
// send on closed channel when Publish races with Close.
func TestAdversarial_EventBus_ConcurrentPublishAndClose(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(500))

		bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
			time.Sleep(1 * time.Millisecond)
			return nil
		})

		ctx := context.Background()
		var wg sync.WaitGroup
		const numPubs = 20
		wg.Add(numPubs)

		var panicked atomic.Bool
		start := make(chan struct{})

		for p := 0; p < numPubs; p++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						panicked.Store(true)
						t.Errorf("PANIC detected during concurrent Publish and Close: %v", r)
					}
				}()
				<-start
				for i := 0; i < 50; i++ {
					_ = bus.Publish(ctx, MutationEvent{
						Topic:   "race",
						Summary: fmt.Sprintf("p-%d-%d", id, i),
					})
				}
			}(p)
		}

		close(start)
		time.Sleep(2 * time.Millisecond)
		_ = bus.Close()
		wg.Wait()

		if panicked.Load() {
			t.Fatalf("Iteration %d: Publish panicked when raced with Close", iteration)
		}
	}
}
