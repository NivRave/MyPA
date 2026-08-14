package tests

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

func TestE2E_RabbitMQIntegration(t *testing.T) {
	ctx := context.Background()

	// 1. Start RabbitMQ Testcontainer
	rabbitContainer, err := rabbitmq.Run(ctx,
		"rabbitmq:3.12-management-alpine",
		rabbitmq.WithAdminPassword("guest"),
	)
	require.NoError(t, err)

	// Clean up the container
	defer func() {
		if err := rabbitContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	}()

	// 2. Get the AMQP URL
	amqpURL, err := rabbitContainer.AmqpURL(ctx)
	require.NoError(t, err)

	// Add credentials to the URL (guest:guest is default)
	// The AmqpURL method might return something like "amqp://localhost:..."
	// testcontainers-go usually formats it properly, but let's verify.

	// Wait a bit for RabbitMQ to be fully ready
	time.Sleep(2 * time.Second)

	// 3. Setup Publisher
	publisher, err := broker.NewPublisher(amqpURL, "test-queue")
	require.NoError(t, err, "Failed to connect Publisher")
	defer publisher.Close()

	// 4. Setup Consumer
	consumer, err := broker.NewConsumer(amqpURL, "test-queue")
	require.NoError(t, err, "Failed to connect Consumer")
	defer consumer.Close()

	// 5. Create a test message
	testMsg := models.Message{
		ID:        "e2e-msg-1",
		UserID:    "e2e-user",
		ChatID:    "e2e-chat",
		Text:      "Test E2E message",
		Source:    "telegram",
		Timestamp: time.Now(),
	}

	// 6. Start the consumer in a separate goroutine
	msgChan := make(chan models.Message, 1)
	errChan := make(chan error, 1)

	go func() {
		err := consumer.Consume(func(msg models.Message) error {
			msgChan <- msg
			return nil
		})
		if err != nil {
			errChan <- err
		}
	}()

	// Give consumer time to bind queue
	time.Sleep(1 * time.Second)

	// 7. Publish message
	err = publisher.Publish(ctx, testMsg)
	require.NoError(t, err, "Failed to publish message")

	// 8. Assert message was received
	select {
	case receivedMsg := <-msgChan:
		assert.Equal(t, testMsg.ID, receivedMsg.ID)
		assert.Equal(t, testMsg.Text, receivedMsg.Text)
		assert.Equal(t, testMsg.UserID, receivedMsg.UserID)
	case err := <-errChan:
		t.Fatalf("Consumer returned error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for message to be consumed")
	}
}
