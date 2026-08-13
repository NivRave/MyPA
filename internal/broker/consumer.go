package broker

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer handles receiving messages from RabbitMQ.
type Consumer struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	queue   amqp.Queue
}

// NewConsumer creates a new RabbitMQ consumer and ensures the queue exists.
func NewConsumer(amqpURL, queueName string) (*Consumer, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Declare durable queue
	q, err := ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	// Set prefetch count to 1 for fair dispatch
	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to set QoS: %w", err)
	}

	return &Consumer{
		conn:    conn,
		channel: ch,
		queue:   q,
	}, nil
}

// Consume starts listening for messages and passes them to the handler.
// This function blocks until the channel is closed or context is cancelled.
func (c *Consumer) Consume(handler func(models.Message) error) error {
	msgs, err := c.channel.Consume(
		c.queue.Name,
		"",    // consumer tag
		false, // auto-ack (we use manual acks)
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	for d := range msgs {
		var msg models.Message
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("failed to unmarshal message", "error", err, "body", string(d.Body))
			// Reject and drop unparseable messages
			_ = d.Reject(false)
			continue
		}

		// Process the message
		if err := handler(msg); err != nil {
			slog.Error("handler failed to process message", "error", err, "message_id", msg.ID)
			// Nack and requeue on error
			_ = d.Nack(false, true)
		} else {
			// Ack on success
			_ = d.Ack(false)
		}
	}

	return nil
}

// Close closes the channel and connection.
func (c *Consumer) Close() error {
	if err := c.channel.Close(); err != nil {
		c.conn.Close()
		return err
	}
	return c.conn.Close()
}
