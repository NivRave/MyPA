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
	isClosed   bool
	closedChan chan struct{}
}

// NewConsumer creates a consumer with DLQ topology setup and bounded retries.
func NewConsumer(amqpURL, queueName string, maxRetries ...int) (*Consumer, error) {
	limit := DefaultMaxRetries
	if len(maxRetries) > 0 && maxRetries[0] > 0 {
		limit = maxRetries[0]
	}

	c := &Consumer{
		amqpURL:    amqpURL,
		queueName:  queueName,
		maxRetries: limit,
		closedChan: make(chan struct{}),
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

	// Prefetch count = 1 for fair dispatch
	if err := ch.Qos(1, 0, false); err != nil {
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
	return c.consumeLoop(func(d amqp.Delivery) {
		var msg models.Message
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("unparseable message rejected directly to DLQ", "error", err, "body", string(d.Body))
			// Fatal poison pill: reject without requeue -> routes directly to mypa.dlx
			_ = d.Reject(false)
			return
		}

		if err := handler(msg); err != nil {
			c.handleFailure(d, err)
		} else {
			_ = d.Ack(false)
		}
	})
}

// ConsumeRaw starts consuming raw bytes with supervision and auto-reconnect.
func (c *Consumer) ConsumeRaw(handler func([]byte) error) error {
	return c.consumeLoop(func(d amqp.Delivery) {
		if err := handler(d.Body); err != nil {
			c.handleFailure(d, err)
		} else {
			_ = d.Ack(false)
		}
	})
}

func (c *Consumer) consumeLoop(processDelivery func(amqp.Delivery)) error {
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

		slog.Info("consumer listening for messages", "queue", qName)
		for d := range msgs {
			processDelivery(d)
		}

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

	if retries+1 >= c.maxRetries {
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
