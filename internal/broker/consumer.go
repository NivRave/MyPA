package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer handles message consumption with auto-reconnection and 3-attempt DLQ routing.
type Consumer struct {
	mu         sync.Mutex
	amqpURL    string
	queueName  string
	conn       *amqp.Connection
	channel    *amqp.Channel
	queue      amqp.Queue
	maxRetries int
	prefetch   int
	isClosed   bool
	closedChan chan struct{}
}

// DefaultPrefetch bounds how many deliveries are processed concurrently (P2-8). Messages from
// the same sender are still processed one at a time, in order.
const DefaultPrefetch = 4

// ConsumerOption configures a Consumer.
type ConsumerOption func(*Consumer)

// WithPrefetch sets the AMQP prefetch (QoS) count = max deliveries in flight across all senders.
func WithPrefetch(n int) ConsumerOption {
	return func(c *Consumer) {
		if n > 0 {
			c.prefetch = n
		}
	}
}

// WithMaxRetries sets the number of delivery attempts before dead-lettering.
func WithMaxRetries(n int) ConsumerOption {
	return func(c *Consumer) {
		if n > 0 {
			c.maxRetries = n
		}
	}
}

// NewConsumer creates a consumer with DLQ topology setup and bounded retries.
func NewConsumer(amqpURL, queueName string, maxRetries ...int) (*Consumer, error) {
	var opts []ConsumerOption
	if len(maxRetries) > 0 {
		opts = append(opts, WithMaxRetries(maxRetries[0]))
	}
	return NewConsumerWithOptions(amqpURL, queueName, opts...)
}

// NewConsumerWithOptions creates a consumer with DLQ topology, bounded retries and a prefetch
// window. Deliveries run in parallel across senders and in order per sender (P2-8).
func NewConsumerWithOptions(amqpURL, queueName string, opts ...ConsumerOption) (*Consumer, error) {
	c := &Consumer{
		amqpURL:    amqpURL,
		queueName:  queueName,
		maxRetries: DefaultMaxRetries,
		prefetch:   DefaultPrefetch,
		closedChan: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}

	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Consumer) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return ErrPublisherClosed
	}

	conn, err := amqp.Dial(c.amqpURL)
	if err != nil {
		return fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}

	q, err := DeclareDLQTopology(ch, c.queueName)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}

	// Prefetch window = max deliveries in flight; per-sender ordering is enforced by the
	// keyed dispatcher in consumeLoop (P2-8).
	if err := ch.Qos(c.prefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	if c.channel != nil && !c.channel.IsClosed() {
		_ = c.channel.Close()
	}
	if c.conn != nil && !c.conn.IsClosed() {
		_ = c.conn.Close()
	}

	c.conn = conn
	c.channel = ch
	c.queue = q
	return nil
}

// Consume starts consuming models.Message items with supervision and auto-reconnect.
func (c *Consumer) Consume(handler func(models.Message) error) error {
	return c.ConsumeWithInfo(func(msg models.Message, _ DeliveryInfo) error { return handler(msg) })
}

// ConsumeWithInfo is like Consume but also tells the handler which delivery attempt it is
// processing, so it can e.g. stay silent on intermediate retryable failures.
// Handler errors wrapping ErrNonRetryable are acked (never redelivered).
func (c *Consumer) ConsumeWithInfo(handler func(models.Message, DeliveryInfo) error) error {
	return c.consumeLoop(func(d amqp.Delivery) (string, func()) {
		var msg models.Message
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			return "", func() {
				slog.Error("unparseable message rejected directly to DLQ", "error", err, "body", string(d.Body))
				// Fatal poison pill: reject without requeue -> routes directly to mypa.dlx
				_ = d.Reject(false)
			}
		}
		// One serial lane per sender: parallel across users, ordered within a user.
		return deliveryKey(msg.Source, msg.UserID), func() {
			err := callContained(func() error {
				return handler(msg, deliveryInfo(getRetryCount(d.Headers), c.maxRetries))
			})
			c.settle(d, err)
		}
	})
}

// ConsumeRaw starts consuming raw bytes with supervision and auto-reconnect. Raw payloads have
// no sender key, so they are processed serially (one lane) in arrival order.
func (c *Consumer) ConsumeRaw(handler func([]byte) error) error {
	return c.consumeLoop(func(d amqp.Delivery) (string, func()) {
		return "", func() {
			c.settle(d, callContained(func() error { return handler(d.Body) }))
		}
	})
}

// settle acks a successful delivery or applies the failure policy (retry / DLQ / ack).
func (c *Consumer) settle(d amqp.Delivery, err error) {
	if err != nil {
		c.handleFailure(d, err)
		return
	}
	_ = d.Ack(false)
}

// callContained turns a handler panic into an error, so the delivery goes through the normal
// retry/DLQ path instead of staying unacked and permanently occupying a prefetch slot.
func callContained(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return fn()
}

// consumeLoop receives deliveries and hands each to a keyed dispatcher: route returns the
// ordering key and the work for a delivery. Deliveries with different keys run concurrently
// (bounded by the prefetch window); deliveries with the same key run in arrival order.
func (c *Consumer) consumeLoop(route func(amqp.Delivery) (key string, task func())) error {
	dispatcher := newKeyedDispatcher()
	defer dispatcher.Wait()

	for {
		c.mu.Lock()
		if c.isClosed {
			c.mu.Unlock()
			return nil
		}
		ch := c.channel
		qName := c.queue.Name
		c.mu.Unlock()

		msgs, err := ch.Consume(
			qName,
			"",    // consumer tag
			false, // manual acks
			false, // exclusive
			false, // no-local
			false, // no-wait
			nil,   // args
		)
		if err != nil {
			slog.Warn("failed to register consumer channel, attempting reconnect", "queue", qName, "error", err)
			if err := c.reconnectWithBackoff(); err != nil {
				return err
			}
			continue
		}

		slog.Info("consumer listening for messages", "queue", qName, "prefetch", c.prefetch)
		for d := range msgs {
			key, task := route(d)
			dispatcher.Submit(key, task)
		}
		// Let in-flight work settle before reconnecting so a redelivered message is never
		// processed concurrently with its still-running original.
		dispatcher.Wait()

		// Channel closed or connection dropped
		c.mu.Lock()
		closed := c.isClosed
		c.mu.Unlock()
		if closed {
			return nil
		}

		slog.Warn("consumer channel closed, attempting auto-reconnect", "queue", qName)
		if err := c.reconnectWithBackoff(); err != nil {
			return err
		}
	}
}

func (c *Consumer) reconnectWithBackoff() error {
	backoff := 250 * time.Millisecond
	maxBackoff := 5 * time.Second

	for {
		select {
		case <-c.closedChan:
			return nil
		case <-time.After(backoff):
		}

		c.mu.Lock()
		closed := c.isClosed
		c.mu.Unlock()
		if closed {
			return nil
		}

		if err := c.connect(); err == nil {
			slog.Info("consumer reconnected successfully", "queue", c.queueName)
			return nil
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Consumer) handleFailure(d amqp.Delivery, handlerErr error) {
	retries := getRetryCount(d.Headers)

	switch decideFailure(handlerErr, retries, c.maxRetries) {
	case actionAck:
		slog.Error("non-retryable handler failure after side effects; acking without redelivery",
			"queue", c.queue.Name,
			"attempt", retries+1,
			"error", handlerErr,
		)
		_ = d.Ack(false)
		return
	case actionDeadLetter:
		slog.Error("message delivery failed, max retries reached; rejecting to DLQ",
			"queue", c.queue.Name,
			"retries", retries+1,
			"max_retries", c.maxRetries,
			"error", handlerErr,
		)
		// Reject without requeue -> RabbitMQ directs to DLX (mypa.dlx -> <queue>.dlq)
		_ = d.Reject(false)
		return
	}

	newRetries := retries + 1
	headers := d.Headers
	if headers == nil {
		headers = make(amqp.Table)
	}
	headers[HeaderRetries] = newRetries
	headers[HeaderLastError] = handlerErr.Error()

	slog.Warn("handler error, requeuing with incremented retry count",
		"queue", c.queue.Name,
		"attempt", newRetries,
		"max_retries", c.maxRetries,
		"error", handlerErr,
	)

	// Exponential backoff delay (200ms, 400ms, 800ms) before republishing
	time.Sleep(time.Duration(1<<uint(retries)) * 200 * time.Millisecond)

	c.mu.Lock()
	defer c.mu.Unlock()

	pubErr := c.channel.PublishWithContext(
		context.Background(),
		"",           // default exchange
		c.queue.Name, // route back to primary queue
		false,
		false,
		amqp.Publishing{
			Headers:         headers,
			ContentType:     d.ContentType,
			ContentEncoding: d.ContentEncoding,
			DeliveryMode:    d.DeliveryMode,
			Priority:        d.Priority,
			CorrelationId:   d.CorrelationId,
			ReplyTo:         d.ReplyTo,
			Expiration:      d.Expiration,
			MessageId:       d.MessageId,
			Timestamp:       d.Timestamp,
			Type:            d.Type,
			UserId:          d.UserId,
			AppId:           d.AppId,
			Body:            d.Body,
		},
	)

	if pubErr != nil {
		slog.Error("failed to republish retry message, rejecting to DLQ", "error", pubErr)
		_ = d.Reject(false)
	} else {
		// ACK the original message since its incremented replacement was queued
		_ = d.Ack(false)
	}
}

func getRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	if v, ok := headers[HeaderRetries]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int32:
			return int(val)
		case int64:
			return int(val)
		}
	}
	if v, ok := headers[HeaderDeath]; ok {
		if deathList, ok := v.([]interface{}); ok && len(deathList) > 0 {
			if deathMap, ok := deathList[0].(amqp.Table); ok {
				if count, ok := deathMap["count"].(int64); ok {
					return int(count)
				}
			}
		}
	}
	return 0
}

// Close gracefully closes the consumer channel and connection.
func (c *Consumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return nil
	}
	c.isClosed = true
	close(c.closedChan)

	var errs []error
	if c.channel != nil && !c.channel.IsClosed() {
		if err := c.channel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.conn != nil && !c.conn.IsClosed() {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing consumer: %v", errs)
	}
	return nil
}
