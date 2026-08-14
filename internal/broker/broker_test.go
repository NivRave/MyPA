package broker

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/require"
)

func TestPublishConsumeRoundTrip(t *testing.T) {
	// Skip if no RabbitMQ running locally
	amqpURL := os.Getenv("RABBITMQ_URL")
	if amqpURL == "" {
		t.Skip("RABBITMQ_URL is not set, skipping test")
	}

	queueName := "test.telegram.inbound"

	// 1. Setup Publisher
	pub, err := NewPublisher(amqpURL, queueName)
	require.NoError(t, err, "Failed to create publisher. Is RabbitMQ running?")
	defer pub.Close()

	// 2. Setup Consumer
	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err, "Failed to create consumer")
	defer sub.Close()

	// 3. Test Message
	originalMsg := models.Message{
		ID:        "msg-123",
		UserID:    "user-456",
		ChatID:    "chat-789",
		Text:      "Block 2 hours for deep work tomorrow",
		Source:    "telegram",
		Timestamp: time.Now().Truncate(time.Millisecond), // Truncate because JSON serialization might lose micro/nanoseconds
	}

	// 4. Start Consumer in a goroutine
	var receivedMsg models.Message
	var wg sync.WaitGroup
	wg.Add(1)

	// Context to timeout the test if message is never received
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Close the consumer channel after receiving 1 message to break out of Consume loop
		err := sub.Consume(func(msg models.Message) error {
			receivedMsg = msg
			wg.Done()
			return nil
		})
		if err != nil {
			t.Logf("Consumer exited with error (expected on close): %v", err)
		}
	}()

	// 5. Publish Message
	err = pub.Publish(ctx, originalMsg)
	require.NoError(t, err, "Failed to publish message")

	// 6. Wait for message or timeout
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		// Success
	case <-ctx.Done():
		t.Fatal("Timeout waiting for message")
	}

	// 7. Verify Message
	require.Equal(t, originalMsg.ID, receivedMsg.ID)
	require.Equal(t, originalMsg.Text, receivedMsg.Text)
	require.Equal(t, originalMsg.UserID, receivedMsg.UserID)
	require.True(t, originalMsg.Timestamp.Equal(receivedMsg.Timestamp), "Timestamps should match")
}
