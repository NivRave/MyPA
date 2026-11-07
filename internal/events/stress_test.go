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

// TestEventBus_Stress_100ConcurrentPublishers stress-tests 100 concurrent publishers
// publishing across both sync and async lanes simultaneously.
func TestEventBus_Stress_100ConcurrentPublishers(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(8), WithQueueCapacity(10000))
	defer bus.Close()

	const (
		numPublishers      = 100
		eventsPerPublisher = 50
		totalEvents        = numPublishers * eventsPerPublisher
	)

	var syncCount atomic.Int64
	var asyncCount atomic.Int64

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		syncCount.Add(1)
		return nil
	})

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		asyncCount.Add(1)
		return nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(numPublishers)

	startSignal := make(chan struct{})

	for i := 0; i < numPublishers; i++ {
		go func(pubID int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < eventsPerPublisher; j++ {
				err := bus.Publish(ctx, MutationEvent{
					Topic:   "stress",
					Summary: fmt.Sprintf("pub-%d-event-%d", pubID, j),
				})
				if err != nil {
					t.Errorf("publish failed: %v", err)
				}
			}
		}(i)
	}

	close(startSignal)
	wg.Wait()

	// Sync count must immediately equal totalEvents
	assert.Equal(t, int64(totalEvents), syncCount.Load(), "All sync handlers must have executed synchronously")

	// Async lane worker pool should process all within reasonable timeout
	assert.Eventually(t, func() bool {
		return asyncCount.Load() == int64(totalEvents)
	}, 10*time.Second, 50*time.Millisecond, "All async tasks must be drained and processed")

	assert.Equal(t, uint64(0), bus.DroppedCount(), "No events should be dropped with 10k capacity")
}

// TestEventBus_Stress_SyncDeterministicExecution verifies that SubscribeSync handlers
// execute strictly in-sequence and complete before Publish returns.
func TestEventBus_Stress_SyncDeterministicExecution(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	var orderMu sync.Mutex
	executionLog := make([]string, 0, 300)

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		orderMu.Lock()
		executionLog = append(executionLog, ev.Summary+"-sync1")
		orderMu.Unlock()
		return nil
	})

	bus.SubscribeSync(func(ctx context.Context, ev MutationEvent) error {
		orderMu.Lock()
		executionLog = append(executionLog, ev.Summary+"-sync2")
		orderMu.Unlock()
		return nil
	})

	ctx := context.Background()
	const numEvents = 50
	for i := 0; i < numEvents; i++ {
		tag := fmt.Sprintf("ev-%02d", i)
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "ordered",
			Summary: tag,
		})
		require.NoError(t, err)

		// Verification: Immediately after Publish returns, both sync handlers must have appended
		orderMu.Lock()
		currentLen := len(executionLog)
		expectedLen := (i + 1) * 2
		assert.Equal(t, expectedLen, currentLen, "Sync handlers must finish before Publish returns")
		assert.Equal(t, tag+"-sync1", executionLog[expectedLen-2])
		assert.Equal(t, tag+"-sync2", executionLog[expectedLen-1])
		orderMu.Unlock()
	}
}

// TestEventBus_Stress_AsyncNonBlockingLatency verifies that slow async handlers
// (simulating 500ms network latency) do NOT block Publish (<2ms return latency).
func TestEventBus_Stress_AsyncNonBlockingLatency(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(100))
	defer bus.Close()

	var slowStarted atomic.Int64
	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		slowStarted.Add(1)
		time.Sleep(500 * time.Millisecond) // Simulated slow external Telegram / Webhook network latency
		return nil
	})

	ctx := context.Background()
	const numPublishes = 20

	for i := 0; i < numPublishes; i++ {
		start := time.Now()
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "latency",
			Summary: fmt.Sprintf("latency-event-%d", i),
		})
		duration := time.Since(start)

		require.NoError(t, err)
		assert.Less(t, duration, 20*time.Millisecond, "Publish should return almost immediately without waiting for 500ms async handler")
	}
}

// TestEventBus_Stress_ShutdownDraining_100Tasks verifies that bus.Close(ctx) while
// 100 tasks are queued drains and completes all tasks without loss or leaks.
func TestEventBus_Stress_ShutdownDraining_100Tasks(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(2), WithQueueCapacity(200))

	blockWorker := make(chan struct{})
	var processedCount atomic.Int64

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		if ev.Summary == "blocker" {
			<-blockWorker
			return nil
		}
		time.Sleep(5 * time.Millisecond)
		processedCount.Add(1)
		return nil
	})

	ctx := context.Background()

	// Fill the workers with blockers first
	_ = bus.Publish(ctx, MutationEvent{Topic: "drain", Summary: "blocker"})
	_ = bus.Publish(ctx, MutationEvent{Topic: "drain", Summary: "blocker"})
	time.Sleep(50 * time.Millisecond)

	// Now queue 100 regular tasks
	const queuedTasks = 100
	for i := 0; i < queuedTasks; i++ {
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "drain",
			Summary: fmt.Sprintf("drain-task-%d", i),
		})
		require.NoError(t, err)
	}

	// Release blocker workers
	close(blockWorker)

	// Initiate graceful shutdown
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := bus.Close(drainCtx)
	require.NoError(t, err, "bus.Close must succeed within timeout")

	assert.Equal(t, int64(queuedTasks), processedCount.Load(), "All 100 queued tasks must be drained and executed")
}

// TestEventBus_Stress_ConcurrentPublishAndClose tests concurrent publishing while Close is triggered.
// This stress test exposes if sends on a closed channel occur without synchronization.
func TestEventBus_Stress_ConcurrentPublishAndClose(t *testing.T) {
	for iteration := 0; iteration < 5; iteration++ {
		bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(100))

		var published atomic.Int64
		bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
			time.Sleep(2 * time.Millisecond)
			return nil
		})

		ctx := context.Background()
		stopPublishers := make(chan struct{})
		var wg sync.WaitGroup

		for p := 0; p < 20; p++ {
			wg.Add(1)
			go func(pubID int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stopPublishers:
						return
					default:
					}
					_ = bus.Publish(ctx, MutationEvent{
						Topic:   "race",
						Summary: fmt.Sprintf("p-%d-%d", pubID, i),
					})
					published.Add(1)
					time.Sleep(100 * time.Microsecond)
				}
			}(p)
		}

		time.Sleep(15 * time.Millisecond)
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = bus.Close(closeCtx)
		cancel()

		close(stopPublishers)
		wg.Wait()
	}
}

// TestEventBus_Stress_PublishDuringClose_RaceTrap aggressively triggers the race window
// between !b.closed.Load() and sending to b.asyncQueue during bus.Close().
func TestEventBus_Stress_PublishDuringClose_RaceTrap(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(50))
		bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
			return nil
		})

		ctx := context.Background()
		var wg sync.WaitGroup

		for p := 0; p < 50; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 500; i++ {
					_ = bus.Publish(ctx, MutationEvent{Topic: "trap", Summary: "trap"})
				}
			}()
		}

		_ = bus.Close()
		wg.Wait()
	}
}

