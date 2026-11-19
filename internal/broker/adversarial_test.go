package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_SafePublisher_50Goroutines tests SafePublisher under heavy concurrent load
// with 50 concurrent goroutines publishing 10 messages each (500 total) with deterministic confirms.
func TestAdversarial_SafePublisher_50Goroutines(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "adversarial.concurrency.50g"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	const numGoroutines = 50
	const msgsPerGoroutine = 10
	totalExpected := numGoroutines * msgsPerGoroutine

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	startGate := make(chan struct{})

	var successCount atomic.Int64
	var failCount atomic.Int64

	for g := 0; g < numGoroutines; g++ {
		go func(routineID int) {
			defer wg.Done()
			<-startGate
			for m := 0; m < msgsPerGoroutine; m++ {
				msg := channel.Inbound{
					MessageID: fmt.Sprintf("adv-msg-%d-%d", routineID, m),
					Text:      "adversarial stress payload",
					SentAt:    time.Now(),
				}
				if err := pub.Publish(ctx, msg); err != nil {
					failCount.Add(1)
					t.Errorf("routine %d publish %d failed: %v", routineID, m, err)
				} else {
					successCount.Add(1)
				}
			}
		}(g)
	}

	close(startGate)
	wg.Wait()

	require.Equal(t, int64(0), failCount.Load(), "Zero publishes should fail under normal broker operation")
	require.Equal(t, int64(totalExpected), successCount.Load(), "All 500 messages must be confirmed by broker")

	// Consume all 500 messages to verify zero frame corruption or message loss
	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err)
	defer sub.Close()

	var receivedCount atomic.Int64
	subCtx, subCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer subCancel()

	go func() {
		_ = sub.Consume(func(m channel.Inbound) error {
			if receivedCount.Add(1) == int64(totalExpected) {
				subCancel()
			}
			return nil
		})
	}()

	<-subCtx.Done()
	assert.Equal(t, int64(totalExpected), receivedCount.Load(), "Consumer must receive all 500 uncorrupted messages")
}

// TestAdversarial_SafePublisher_ConcurrentReconnectStress tests publisher resilience
// when channel/connection drops during concurrent publishing.
func TestAdversarial_SafePublisher_ConcurrentReconnectStress(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "adversarial.reconnect.stress"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Initial publish
	err = pub.Publish(ctx, channel.Inbound{MessageID: "pre-drop-msg"})
	require.NoError(t, err)

	// Simulate connection severed
	pub.mu.Lock()
	if pub.conn != nil && !pub.conn.IsClosed() {
		_ = pub.conn.Close()
	}
	pub.mu.Unlock()

	// Concurrent publishers attempt to publish during/after reconnect
	const workers = 10
	var wg sync.WaitGroup
	wg.Add(workers)

	var successCount atomic.Int64
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				// Reconnect might take up to a few seconds
				err := pub.Publish(ctx, channel.Inbound{
					MessageID: fmt.Sprintf("post-drop-%d-%d", id, j),
					Text:      "reconnected payload",
				})
				if err == nil {
					successCount.Add(1)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	assert.Greater(t, successCount.Load(), int64(0), "Publisher should successfully reconnect and continue publishing")
}
