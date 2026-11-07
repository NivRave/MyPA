package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrPublisherClosed   = errors.New("amqp publisher is closed")
	ErrBrokerUnavailable = errors.New("rabbitmq broker is currently unavailable")
	ErrMessageNacked     = errors.New("rabbitmq broker nacked published message")
)

// SafePublisher wraps an AMQP connection and channel with mutex synchronization,
// publisher confirms, and automatic background/on-demand reconnection.
type SafePublisher struct {
	mu         sync.Mutex
	amqpURL    string
	queueName  string
	conn       *amqp.Connection
	channel    *amqp.Channel
	queue      amqp.Queue
	isClosed   bool
	closedChan chan struct{}
}

// Publisher is a type alias for SafePublisher to provide 100% backward compatibility.
type Publisher = SafePublisher

// NewSafePublisher initializes a thread-safe publisher, establishes the connection,
// puts the channel in confirm mode, declares the topology, and starts connection monitoring.
func NewSafePublisher(amqpURL, queueName string) (*SafePublisher, error) {
	p := &SafePublisher{
		amqpURL:    amqpURL,
		queueName:  queueName,
		closedChan: make(chan struct{}),
	}

	if err := p.connect(); err != nil {
		return nil, err
	}

	go p.monitorConnection()
	return p, nil
}

// NewPublisher preserves the existing constructor signature.
func NewPublisher(amqpURL, queueName string) (*Publisher, error) {
	return NewSafePublisher(amqpURL, queueName)
}

func (p *SafePublisher) connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed {
		return ErrPublisherClosed
	}

	conn, err := amqp.Dial(p.amqpURL)
	if err != nil {
		return fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}

	// 1. Enable Publisher Confirms
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("failed to enable publisher confirms: %w", err)
	}

	// 2. Declare DLX, DLQ, and Primary Queue
	q, err := DeclareDLQTopology(ch, p.queueName)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}

	// Close old instances if reconnecting
	if p.channel != nil && !p.channel.IsClosed() {
		_ = p.channel.Close()
	}
	if p.conn != nil && !p.conn.IsClosed() {
		_ = p.conn.Close()
	}

	p.conn = conn
	p.channel = ch
	p.queue = q
	return nil
}

func (p *SafePublisher) monitorConnection() {
	for {
		p.mu.Lock()
		if p.isClosed {
			p.mu.Unlock()
			return
		}
		closeChan := make(chan *amqp.Error, 1)
		if p.conn != nil && !p.conn.IsClosed() {
			p.conn.NotifyClose(closeChan)
		}
		p.mu.Unlock()

		select {
		case <-p.closedChan:
			return
		case err, ok := <-closeChan:
			if !ok || p.isClosed {
				return
			}
			slog.Warn("RabbitMQ publisher connection lost, initiating background reconnect",
				"queue", p.queueName,
				"error", err,
			)
			p.reconnectLoop()
		}
	}
}

func (p *SafePublisher) reconnectLoop() {
	backoff := 250 * time.Millisecond
	maxBackoff := 5 * time.Second

	for {
		select {
		case <-p.closedChan:
			return
		case <-time.After(backoff):
		}

		p.mu.Lock()
		closed := p.isClosed
		p.mu.Unlock()
		if closed {
			return
		}

		if err := p.connect(); err == nil {
			slog.Info("RabbitMQ publisher successfully reconnected and topology verified", "queue", p.queueName)
			return
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		slog.Warn("RabbitMQ publisher reconnect failed, retrying...", "backoff", backoff)
	}
}

// Publish serializes and publishes message v with mutex protection and publisher confirms.
func (p *SafePublisher) Publish(ctx context.Context, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed {
		return ErrPublisherClosed
	}

	// On-demand reconnect if connection/channel dropped
	if p.conn == nil || p.conn.IsClosed() || p.channel == nil || p.channel.IsClosed() {
		// Temporary unlock to call connect which acquires the lock
		p.mu.Unlock()
		err := p.connect()
		p.mu.Lock()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBrokerUnavailable, err)
		}
	}

	// Publish with deferred confirmation
	deferredConf, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		"",           // default exchange
		p.queue.Name, // routing key
		false,        // mandatory
		false,        // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
			Timestamp:    time.Now().UTC(),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish message to queue %s: %w", p.queue.Name, err)
	}

	// Await broker confirmation within context deadline
	acked, err := deferredConf.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("publisher confirm error: %w", err)
	}
	if !acked {
		return ErrMessageNacked
	}

	return nil
}

// Close gracefully shuts down the publisher.
func (p *SafePublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed {
		return nil
	}
	p.isClosed = true
	close(p.closedChan)

	var errs []error
	if p.channel != nil && !p.channel.IsClosed() {
		if err := p.channel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.conn != nil && !p.conn.IsClosed() {
		if err := p.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing publisher: %v", errs)
	}
	return nil
}
