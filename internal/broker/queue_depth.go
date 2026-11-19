package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// probeHandshakeTimeout bounds the AMQP handshake when the caller's context has no deadline.
const probeHandshakeTimeout = 30 * time.Second

// QueueDepth returns the number of messages ready in a queue. It is a read-only probe for the
// /doctor health check (V4-17): it opens its own short-lived connection, declares nothing
// (a passive declare fails for a queue that does not exist) and consumes nothing.
//
// ctx bounds the whole probe: the TCP connect, the AMQP handshake (through a deadline on the
// socket) and everything after it (the connection is closed when ctx ends, which unblocks any
// call in flight). A broker that accepts the connection and then goes silent therefore cannot
// hold the caller, a goroutine or a socket beyond ctx.
func QueueDepth(ctx context.Context, amqpURL, queue string) (int, error) {
	if _, err := amqp.ParseURI(amqpURL); err != nil {
		// Never echo the URL: it carries the broker password.
		return 0, errors.New("the RabbitMQ address is not a valid AMQP URL")
	}
	conn, err := amqp.DialConfig(amqpURL, amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			var d net.Dialer
			c, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			// A custom Dial replaces the library's default one, which is what normally sets the
			// handshake deadline. The library clears the deadline once the handshake completes.
			deadline, ok := ctx.Deadline()
			if !ok {
				deadline = time.Now().Add(probeHandshakeTimeout)
			}
			if err := c.SetDeadline(deadline); err != nil {
				_ = c.Close()
				return nil, err
			}
			return c, nil
		},
	})
	if err != nil {
		return 0, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

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
