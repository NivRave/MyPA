package broker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

// TestSafePublisher_Stress_50ConcurrentGoroutines stresses SafePublisher with 50+ concurrent
// goroutines publishing simultaneously with publisher confirms enabled.
func TestSafePublisher_Stress_50ConcurrentGoroutines(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.stress.50goroutines"
	pub, err := NewSafePublisher(amqpURL, queueName)
	require.NoError(t, err)
	defer pub.Close()

	const numGoroutines = 50
	const msgsPerGoroutine = 20
	totalExpected := numGoroutines * msgsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	startSignal := make(chan struct{})

	for g := 0; g < numGoroutines; g++ {
		go func(routineID int) {
			defer wg.Done()
			<-startSignal

			for m := 0; m < msgsPerGoroutine; m++ {
				msg := models.Message{
					ID:        fmt.Sprintf("msg-stress-%d-%d", routineID, m),
					Text:      "payload-50-goroutines-stress",
					Timestamp: time.Now(),
				}
				if err := pub.Publish(ctx, msg); err != nil {
					t.Errorf("routine %d publish failed: %v", routineID, err)
				}
			}
		}(g)
	}

	close(startSignal)
	wg.Wait()

	// Verify all messages arrived in queue
	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err)
	defer sub.Close()

	var countMu sync.Mutex
	receivedCount := 0
	subCtx, subCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer subCancel()

	go func() {
		_ = sub.Consume(func(m models.Message) error {
			countMu.Lock()
			receivedCount++
			cur := receivedCount
			countMu.Unlock()

			if cur == totalExpected {
				subCancel()
			}
			return nil
		})
	}()

	<-subCtx.Done()

	countMu.Lock()
	finalCount := receivedCount
	countMu.Unlock()

	require.Equal(t, totalExpected, finalCount, "All 1,000 published messages from 50 concurrent goroutines must be delivered")
}

// TestConsumer_Stress_MalformedJSON_BatchReject verifies multiple malformed JSON payloads
// are immediately rejected directly to DLQ without entering an infinite requeue loop.
func TestConsumer_Stress_MalformedJSON_BatchReject(t *testing.T) {
	amqpURL, cleanup := setupTestRabbitMQ(t)
	defer cleanup()

	queueName := "test.stress.malformed.batch"
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()

	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()

	_, err = DeclareDLQTopology(ch, queueName)
	require.NoError(t, err)

	const numMalformed = 10
	for i := 0; i < numMalformed; i++ {
		err = ch.PublishWithContext(context.Background(), "", queueName, false, false, amqp.Publishing{
			Body: []byte(fmt.Sprintf("{ malformed-json-payload-number-%d", i)),
		})
		require.NoError(t, err)
	}

	sub, err := NewConsumer(amqpURL, queueName)
	require.NoError(t, err)
	defer sub.Close()

	var handlerCalls int
	go func() {
		_ = sub.Consume(func(m models.Message) error {
			handlerCalls++
			return nil
		})
	}()

	time.Sleep(2 * time.Second)
	require.Equal(t, 0, handlerCalls, "Handler must never be invoked for malformed payloads")

	// Verify DLQ contains all 10 rejected messages
	for i := 0; i < numMalformed; i++ {
		d, ok, err := ch.Get(queueName+".dlq", true)
		require.NoError(t, err)
		require.True(t, ok, fmt.Sprintf("DLQ must contain malformed message %d", i))
		require.Contains(t, string(d.Body), "malformed-json-payload-number")
	}
}
