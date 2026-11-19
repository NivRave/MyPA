package broker

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QueueDepth backs the /doctor queue check (V4-17): it must report how many messages wait in a
// queue and in its dead-letter queue, without consuming them or changing the topology.
func TestQueueDepth_CountsReadyMessagesWithoutConsuming(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const queue = "test.depth.inbound"
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()
	_, err = DeclareDLQTopology(ch, queue)
	require.NoError(t, err)
	require.NoError(t, ch.Confirm(false))

	publish := func(target string, n int) {
		for i := 0; i < n; i++ {
			confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", target, true, false,
				amqp.Publishing{ContentType: "application/json", Body: []byte(`{}`), DeliveryMode: amqp.Persistent})
			require.NoError(t, err)
			ok, err := confirmation.WaitContext(ctx)
			require.NoError(t, err)
			require.True(t, ok)
		}
	}
	publish(queue, 3)
	publish(queue+".dlq", 2)

	depth, err := QueueDepth(ctx, amqpURL, queue)
	require.NoError(t, err)
	assert.Equal(t, 3, depth)

	failed, err := QueueDepth(ctx, amqpURL, queue+".dlq")
	require.NoError(t, err)
	assert.Equal(t, 2, failed)

	again, err := QueueDepth(ctx, amqpURL, queue)
	require.NoError(t, err)
	assert.Equal(t, 3, again, "reading the depth must not consume messages")
}

// A queue that does not exist is an error, and asking must not create it.
func TestQueueDepth_MissingQueueIsAnErrorAndIsNotCreated(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := QueueDepth(ctx, amqpURL, "test.depth.never-declared")
	require.Error(t, err)

	_, err = QueueDepth(ctx, amqpURL, "test.depth.never-declared")
	require.Error(t, err, "the first call must not have created the queue")
}

func TestQueueDepth_UnreachableBrokerIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := QueueDepth(ctx, "amqp://guest:guest@127.0.0.1:1/", InboundMessagesQueue)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "guest:guest", "broker credentials must not appear in the error")
}
