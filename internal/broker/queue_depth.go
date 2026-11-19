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

// QueueState is what a passive look at a queue shows.
type QueueState struct {
	Ready     int // messages waiting to be delivered
	Consumers int // processes currently consuming from the queue
}

// QueueDepth returns the number of messages ready in a queue (see InspectQueue).
func QueueDepth(ctx context.Context, amqpURL, queue string) (int, error) {
	state, err := InspectQueue(ctx, amqpURL, queue)
	return state.Ready, err
}

// InspectQueue reports how many messages wait in a queue and how many consumers are attached.
// It is a read-only probe, used by the /doctor health check (V4-17) and by the restore-and-migrate
// tool to confirm an install is drained and stopped (V4-43): it opens its own short-lived
// connection, declares nothing (a passive declare fails for a queue that does not exist) and
// consumes nothing.
//
// ctx bounds the whole probe: the TCP connect, the AMQP handshake (through a deadline on the
// socket) and everything after it (the connection is closed when ctx ends, which unblocks any
// call in flight). A broker that accepts the connection and then goes silent therefore cannot
// hold the caller, a goroutine or a socket beyond ctx.
func InspectQueue(ctx context.Context, amqpURL, queue string) (QueueState, error) {
	if _, err := amqp.ParseURI(amqpURL); err != nil {
		// Never echo the URL: it carries the broker password.
		return QueueState{}, errors.New("the RabbitMQ address is not a valid AMQP URL")
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
		return QueueState{}, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	ch, err := conn.Channel()
	if err != nil {
		return QueueState{}, fmt.Errorf("open a RabbitMQ channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		return QueueState{}, fmt.Errorf("inspect queue %s: %w", queue, err)
	}
	return QueueState{Ready: q.Messages, Consumers: q.Consumers}, nil
}

// IsQueueMissing reports whether err from InspectQueue or QueueDepth means the queue does not
// exist (as on an install that has never been started), rather than that it could not be read.
func IsQueueMissing(err error) bool {
	var amqpErr *amqp.Error
	return errors.As(err, &amqpErr) && amqpErr.Code == amqp.NotFound
}
