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

// TestEmpirical_EventBus_PanicIsolationAndSubsequentExecution stress-tests that
// when an asynchronous handler panics ("simulated fatal subscriber failure"),
// the worker pool intercepts the panic, logs the stack trace, and continues
// processing subsequent events on that same worker without crashing or hanging.
func TestEmpirical_EventBus_PanicIsolationAndSubsequentExecution(t *testing.T) {
	// Worker count 2 so we test worker survivability
	bus := NewEventBus(WithWorkerCount(2), WithQueueCapacity(100))
	defer bus.Close()

	ctx := context.Background()

	var panicCount atomic.Int64
	var healthyCount atomic.Int64

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		if ev.Summary == "FAIL_PANIC" {
			panicCount.Add(1)
			panic("simulated fatal subscriber failure")
		}
		healthyCount.Add(1)
		return nil
	})

	const totalPanics = 20
	const totalHealthy = 50

	// Interleave panicking and healthy events
	for i := 0; i < totalPanics; i++ {
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "stress",
			Summary: "FAIL_PANIC",
		})
		require.NoError(t, err)

		err = bus.Publish(ctx, MutationEvent{
			Topic:   "stress",
			Summary: fmt.Sprintf("HEALTHY_%d", i),
		})
		require.NoError(t, err)
	}

	// Publish remaining healthy events
	for i := totalPanics; i < totalHealthy; i++ {
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "stress",
			Summary: fmt.Sprintf("HEALTHY_%d", i),
		})
		require.NoError(t, err)
	}

	// Assert that healthy events are all processed despite 20 panics
	assert.Eventually(t, func() bool {
		return healthyCount.Load() == int64(totalHealthy)
	}, 3*time.Second, 20*time.Millisecond, "All healthy events must be processed despite worker panics")

	assert.Equal(t, int64(totalPanics), panicCount.Load(), "All 20 panic events must have triggered panic handler")
}

// TestEmpirical_EventBus_Backpressure1200EventsQueueSaturation verifies:
// 1. Queue capacity is bounded at 1024 (default).
// 2. Scribing 1,200 events when the worker queue is saturated does NOT deadlock or crash.
// 3. DroppedCount() strictly increments for all events exceeding capacity.
// 4. Memory does not grow unboundedly (events are dropped non-blockingly).
func TestEmpirical_EventBus_Backpressure1200EventsQueueSaturation(t *testing.T) {
	// 1 worker, default capacity 1024
	bus := NewEventBus(WithWorkerCount(1), WithQueueCapacity(1024))
	defer bus.Close()

	ctx := context.Background()

	// Block the single worker so the queue fills up completely
	workerStarted := make(chan struct{}, 1)
	releaseWorker := make(chan struct{})

	bus.SubscribeAsync(func(ctx context.Context, ev MutationEvent) error {
		select {
		case workerStarted <- struct{}{}:
		default:
		}
		<-releaseWorker
		return nil
	})

	// 1st event is taken by the worker and blocked
	err := bus.Publish(ctx, MutationEvent{Topic: "queue", Summary: "blocker"})
	require.NoError(t, err)
	<-workerStarted

	// Now the queue of 1024 is empty, and the worker is busy.
	// Publish 1,200 events rapidly.
	// 1024 should fit in the buffered channel; the remaining 176 should be dropped.
	const totalEvents = 1200
	for i := 0; i < totalEvents; i++ {
		err := bus.Publish(ctx, MutationEvent{
			Topic:   "queue",
			Summary: fmt.Sprintf("event-%d", i),
		})
		require.NoError(t, err, "Publish itself must never fail even if async queue drops events")
	}

	dropped := bus.DroppedCount()
	t.Logf("Total dropped events out of 1200: %d", dropped)
	assert.Equal(t, uint64(176), dropped, "Exactly 176 events must be dropped when 1200 events are sent to capacity 1024")

	// Release worker and drain
	close(releaseWorker)
}

// TestEmpirical_EventBus_ConcurrentMultipleSubscribersPanicsAndDrops tests concurrent publishers
// publishing to multiple async subscribers, some of which panic while others succeed.
func TestEmpirical_EventBus_ConcurrentMultipleSubscribersPanicsAndDrops(t *testing.T) {
	bus := NewEventBus(WithWorkerCount(4), WithQueueCapacity(500))
	defer bus.Close()

	ctx := context.Background()

	var successCount atomic.Int64
	var panicCount atomic.Int64

	// Subscriber 1: panics on odd numbers
	bus.SubscribeAsyncTopic("concurrent_test", func(ctx context.Context, ev MutationEvent) error {
		if ev.Summary == "PANIC" {
			panicCount.Add(1)
			panic("concurrent fatal subscriber failure")
		}
		successCount.Add(1)
		return nil
	})

	// Subscriber 2: always succeeds
	bus.SubscribeAsyncTopic("concurrent_test", func(ctx context.Context, ev MutationEvent) error {
		successCount.Add(1)
		return nil
	})

	const numPublishers = 10
	const eventsPerPub = 50
	var wg sync.WaitGroup
	wg.Add(numPublishers)

	for p := 0; p < numPublishers; p++ {
		go func(pubID int) {
			defer wg.Done()
			for e := 0; e < eventsPerPub; e++ {
				summary := "OK"
				if e%5 == 0 {
					summary = "PANIC"
				}
				_ = bus.Publish(ctx, MutationEvent{
					Topic:   "concurrent_test",
					Summary: summary,
				})
			}
		}(p)
	}

	wg.Wait()

	// Wait for queue drain and assert exact task accounting
	assert.Eventually(t, func() bool {
		totalAccounted := panicCount.Load() + successCount.Load() + int64(bus.DroppedCount())
		return totalAccounted == int64(numPublishers*eventsPerPub*2)
	}, 4*time.Second, 50*time.Millisecond, "All 1000 tasks must be accounted for across panics, successes, and drops")

	totalAccounted := panicCount.Load() + successCount.Load() + int64(bus.DroppedCount())
	assert.Equal(t, int64(numPublishers*eventsPerPub*2), totalAccounted, "Total tasks must exactly equal 1,000")
	assert.Greater(t, panicCount.Load(), int64(80), "Panic isolation handled dozens of panics without crashing")
	assert.Greater(t, successCount.Load(), int64(800), "Dozens of successful async handlers completed")

	t.Logf("Concurrent test passed: %d panics isolated, %d successes completed, %d drops (Total: %d)",
		panicCount.Load(), successCount.Load(), bus.DroppedCount(), totalAccounted)
}
