package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBus_GlobalAndTopicSubscribers(t *testing.T) {
	bus := NewEventBus()
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before publish

	ev := MutationEvent{Topic: "test"}
	err := bus.Publish(ctx, ev)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestEventBus_Concurrency(t *testing.T) {
	bus := NewEventBus()
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
