package broker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P2-8 acceptance: user A's slow handler doesn't delay user B; user A's messages stay in order.
func TestConsumer_ParallelAcrossUsers_OrderedPerUser(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queue := "test.parallel.inbound." + time.Now().Format("150405.000000")
	pub, err := NewPublisher(amqpURL, queue)
	require.NoError(t, err)
	defer pub.Close()

	sub, err := NewConsumerWithOptions(amqpURL, queue, WithPrefetch(4))
	require.NoError(t, err)
	defer sub.Close()

	var mu sync.Mutex
	finished := map[string]time.Time{}
	var orderA []string
	allDone := make(chan struct{})

	go func() {
		_ = sub.Consume(func(msg channel.Inbound) error {
			if msg.MessageID == "a1" {
				time.Sleep(2 * time.Second) // user A's slow turn
			}
			mu.Lock()
			defer mu.Unlock()
			finished[msg.MessageID] = time.Now()
			if msg.Sender == "user-a" {
				orderA = append(orderA, msg.MessageID)
			}
			if len(finished) == 3 {
				close(allDone)
			}
			return nil
		})
	}()
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, m := range []channel.Inbound{
		{MessageID: "a1", Sender: "user-a", Channel: "telegram", Text: "slow"},
		{MessageID: "a2", Sender: "user-a", Channel: "telegram", Text: "second"},
		{MessageID: "b1", Sender: "user-b", Channel: "telegram", Text: "fast"},
	} {
		require.NoError(t, pub.Publish(ctx, m))
	}

	select {
	case <-allDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for messages")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, finished["b1"].Before(finished["a1"]), "user B must not wait for user A's slow turn")
	assert.Equal(t, []string{"a1", "a2"}, orderA, "user A's messages must be processed in order")
}
