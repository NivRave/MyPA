package broker

import (
	"context"
	"errors"
	"fmt"
	"net"

	amqp "github.com/rabbitmq/amqp091-go"
)

// QueueDepth returns the number of messages ready in a queue. It is a read-only probe for the
// /doctor health check (V4-17): it opens its own short-lived connection, declares nothing
// (a passive declare fails for a queue that does not exist) and consumes nothing. ctx bounds the
// connection attempt.
func QueueDepth(ctx context.Context, amqpURL, queue string) (int, error) {
	if _, err := amqp.ParseURI(amqpURL); err != nil {
		// Never echo the URL: it carries the broker password.
		return 0, errors.New("the RabbitMQ address is not a valid AMQP URL")
	}
	conn, err := amqp.DialConfig(amqpURL, amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	})
	if err != nil {
		return 0, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ch, err := conn.Channel()
	if err != nil {
		return 0, fmt.Errorf("open a RabbitMQ channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		return 0, fmt.Errorf("inspect queue %s: %w", queue, err)
	}
	return q.Messages, nil
}
