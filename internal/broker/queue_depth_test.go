package broker

import (
	"context"
	"net"
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
	assert.True(t, IsQueueMissing(err), "a missing queue must be told apart from an unreachable broker")
}

func TestQueueDepth_UnreachableBrokerIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := QueueDepth(ctx, "amqp://guest:guest@127.0.0.1:1/", InboundMessagesQueue)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "guest:guest", "broker credentials must not appear in the error")
	assert.False(t, IsQueueMissing(err))
}

// A broker that accepts the connection and then says nothing must not hang the caller: the
// context bounds the whole probe, not only the TCP connect.
func TestQueueDepth_SilentBrokerIsBoundedByTheContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var held []net.Conn
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			held = append(held, conn) // kept open, never answered
		}
	}()
	defer func() {
		_ = listener.Close()
		<-acceptDone
		for _, conn := range held {
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = QueueDepth(ctx, "amqp://guest:guest@"+listener.Addr().String()+"/", InboundMessagesQueue)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "the probe must give up when its context expires")
}

// InspectQueue also reports attached consumers: that is how the restore-and-migrate tool (V4-43)
// tells a stopped install from a running one.
func TestInspectQueue_ReportsConsumers(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const queue = "test.inspect.consumers"
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	_, err = DeclareDLQTopology(ch, queue)
	require.NoError(t, err)

	state, err := InspectQueue(ctx, amqpURL, queue)
	require.NoError(t, err)
	assert.Equal(t, QueueState{Ready: 0, Consumers: 0}, state, "nothing consumes a freshly declared queue")

	_, err = ch.Consume(queue, "test-consumer", false, false, false, false, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		state, err := InspectQueue(ctx, amqpURL, queue)
		return err == nil && state.Consumers == 1
	}, 10*time.Second, 100*time.Millisecond, "an attached consumer must be visible")

	require.NoError(t, ch.Close())
	require.Eventually(t, func() bool {
		state, err := InspectQueue(ctx, amqpURL, queue)
		return err == nil && state.Consumers == 0
	}, 10*time.Second, 100*time.Millisecond, "a consumer that went away must no longer be counted")
}
