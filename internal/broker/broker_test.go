package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

var ( sharedRabbitOnce sync.Once; sharedAmqpURL string; sharedRabbitErr error )
func setupTestRabbitMQ(t *testing.T) (string, func()) { sharedRabbitOnce.Do(func() {
	ctx := context.Background()

	rabbitContainer, err := rabbitmq.Run(ctx,
		"rabbitmq:4.3.7-management-alpine@sha256:eb338b3fc8f5c251028c0022ac0de3fe1db2d599d1c6a08b45898f756f248881",
	)
	if err != nil {
		sharedRabbitErr = fmt.Errorf("failed to start container: %w", err); return
	}

	amqpURL, err := rabbitContainer.AmqpURL(ctx)
	if err != nil {
		sharedRabbitErr = fmt.Errorf("failed to get connection string: %w", err); return
	}

	_ = func() {
		if false { if err := rabbitContainer.Terminate(ctx); err != nil {
			log.Fatalf("failed to terminate container: %s", err) }
		}
	}

	sharedAmqpURL = amqpURL }); if sharedRabbitErr != nil { t.Fatalf("%v", sharedRabbitErr) }; return sharedAmqpURL, func() {}
}

func TestPublishConsumeRoundTrip(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.telegram.inbound"

	// 1. Setup Publisher
	pub, err := NewPublisher(amqpURL, queueName)
	require.NoError(t, err, "Failed to create publisher")
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
		Timestamp: time.Now().Truncate(time.Millisecond),
	}

	// 4. Start Consumer in a goroutine
	var receivedMsg models.Message
	var wg sync.WaitGroup
	wg.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go func() {
		_ = sub.Consume(func(msg models.Message) error {
			receivedMsg = msg
			wg.Done()
			return nil
		})
	}()

	time.Sleep(500 * time.Millisecond)

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
}

func TestSafePublisher_ConcurrentPublish_RaceFree(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.concurrent.publish"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	const numGoroutines = 25
	const msgsPerGoroutine = 10
	totalExpected := numGoroutines * msgsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for g := 0; g < numGoroutines; g++ {
		go func(routineID int) {
			defer wg.Done()
			for m := 0; m < msgsPerGoroutine; m++ {
				msg := models.Message{
					ID:        fmt.Sprintf("msg-%d-%d", routineID, m),
					Text:      "concurrent test payload",
					Timestamp: time.Now(),
				}
				err := pub.Publish(ctx, msg)
				require.NoError(t, err)
			}
		}(g)
	}

	wg.Wait()

	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err)
	defer sub.Close()

	var countMu sync.Mutex
	receivedCount := 0
	subCtx, subCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer subCancel()

	go func() {
		_ = sub.Consume(func(m models.Message) error {
			countMu.Lock()
			receivedCount++
			current := receivedCount
			countMu.Unlock()

			if current == totalExpected {
				subCancel()
			}
			return nil
		})
	}()

	<-subCtx.Done()
	countMu.Lock()
	finalCount := receivedCount
	countMu.Unlock()
	require.Equal(t, totalExpected, finalCount, "All published messages must be safely delivered")
}

func TestDeclareDLQTopology_Verification(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.dlq.topology"
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()

	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()

	q, err := DeclareDLQTopology(ch, queueName)
	require.NoError(t, err)
	require.Equal(t, queueName, q.Name)

	dlq, err := ch.QueueDeclarePassive(queueName+".dlq", true, false, false, false, nil)
	require.NoError(t, err)
	require.Equal(t, queueName+".dlq", dlq.Name)
}

func TestConsumer_PoisonPill_RoutesToDLQ(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.poison.pill"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	sub, err := NewConsumer(amqpURL, queueName, 3)
	require.NoError(t, err)
	defer sub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	failingMsg := models.Message{
		ID:        "poison-123",
		Text:      "always fail",
		Timestamp: time.Now(),
	}

	var attemptsMu sync.Mutex
	attempts := 0
	go func() {
		_ = sub.Consume(func(m models.Message) error {
			attemptsMu.Lock()
			attempts++
			attemptsMu.Unlock()
			return errors.New("simulated database failure")
		})
	}()

	err = pub.Publish(ctx, failingMsg)
	require.NoError(t, err)

	time.Sleep(3 * time.Second)
	attemptsMu.Lock()
	finalAttempts := attempts
	attemptsMu.Unlock()
	require.GreaterOrEqual(t, finalAttempts, 3, "Consumer must attempt at least 3 deliveries before DLQ")

	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()

	d, ok, err := ch.Get(queueName+".dlq", true)
	require.NoError(t, err)
	require.True(t, ok, "Dead-letter queue must contain the rejected message")

	var deadMsg models.Message
	err = json.Unmarshal(d.Body, &deadMsg)
	require.NoError(t, err)
	require.Equal(t, failingMsg.ID, deadMsg.ID)
}

func TestConsumer_MalformedJSON_ImmediateDLQ(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.malformed.json"
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()

	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()

	_, err = DeclareDLQTopology(ch, queueName)
	require.NoError(t, err)

	err = ch.PublishWithContext(context.Background(), "", queueName, false, false, amqp.Publishing{
		Body: []byte("{ invalid-json-payload"),
	})
	require.NoError(t, err)

	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err)
	defer sub.Close()

	go func() {
		_ = sub.Consume(func(m models.Message) error {
			t.Errorf("Handler should never be called for malformed JSON")
			return nil
		})
	}()

	time.Sleep(1 * time.Second)

	d, ok, err := ch.Get(queueName+".dlq", true)
	require.NoError(t, err)
	require.True(t, ok, "Malformed message must immediately land in DLQ without requeue loop")
	require.Equal(t, []byte("{ invalid-json-payload"), d.Body)
}

func TestSafePublisher_AutoReconnect_SimulatedDisconnect(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.publisher.reconnect"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = pub.Publish(ctx, models.Message{ID: "msg-1"})
	require.NoError(t, err)

	pub.mu.Lock()
	_ = pub.channel.Close()
	pub.mu.Unlock()

	err = pub.Publish(ctx, models.Message{ID: "msg-2"})
	require.NoError(t, err, "SafePublisher must auto-reconnect on channel closure")
}

func TestGetRetryCount(t *testing.T) {
	// 1. Nil headers
	require.Equal(t, 0, getRetryCount(nil))

	// 2. Empty headers
	require.Equal(t, 0, getRetryCount(amqp.Table{}))

	// 3. int HeaderRetries
	require.Equal(t, 2, getRetryCount(amqp.Table{HeaderRetries: 2}))

	// 4. int32 HeaderRetries
	require.Equal(t, 3, getRetryCount(amqp.Table{HeaderRetries: int32(3)}))

	// 5. int64 HeaderRetries
	require.Equal(t, 4, getRetryCount(amqp.Table{HeaderRetries: int64(4)}))

	// 6. HeaderDeath fallback
	death := []interface{}{
		amqp.Table{
			"count": int64(5),
		},
	}
	require.Equal(t, 5, getRetryCount(amqp.Table{HeaderDeath: death}))
}

