package broker

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Queue names, shared by every publisher and consumer (BL-5). Each queue gets "<name>.dlq".
const (
	// InboundMessagesQueue carries normalized user messages from every channel (Telegram, WhatsApp)
	// from the gateway to the orchestrator. Was "telegram.inbound" before it also carried WhatsApp.
	InboundMessagesQueue = "inbound.messages"
	// AuditEventsQueue carries audit telemetry from the orchestrator to the audit-worker.
	AuditEventsQueue = "audit.events"
)

const (
	// DefaultDLX is the centralized direct exchange for dead-lettered messages
	DefaultDLX = "mypa.dlx"

	// DefaultMaxRetries is the maximum delivery attempts before dead-lettering
	DefaultMaxRetries = 3

	// AMQP Header keys
	HeaderRetries   = "x-retries"
	HeaderLastError = "x-last-error"
	HeaderDeath     = "x-death"
)

// DeclareDLQTopology idempotently declares the dead-letter exchange, the dead-letter queue,
// and the primary queue configured with dead-letter forwarding arguments.
func DeclareDLQTopology(ch *amqp.Channel, queueName string) (amqp.Queue, error) {
	dlxName := DefaultDLX
	dlqName := queueName + ".dlq"

	// 1. Declare Direct Dead-Letter Exchange
	err := ch.ExchangeDeclare(
		dlxName,
		amqp.ExchangeDirect,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("failed to declare dlx %s: %w", dlxName, err)
	}

	// 2. Declare Dead-Letter Queue (durable, never auto-deleted)
	_, err = ch.QueueDeclare(
		dlqName,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("failed to declare dlq %s: %w", dlqName, err)
	}

	// 3. Bind Dead-Letter Queue to DLX with routing_key = dlqName
	err = ch.QueueBind(
		dlqName,
		dlqName,
		dlxName,
		false,
		nil,
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("failed to bind dlq %s to dlx %s: %w", dlqName, dlxName, err)
	}

	// 4. Declare Primary Queue with DLX arguments
	args := amqp.Table{
		"x-dead-letter-exchange":    dlxName,
		"x-dead-letter-routing-key": dlqName,
		"x-max-priority":            uint8(10),
	}

	q, err := ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		args,
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("failed to declare primary queue %s with dlx: %w", queueName, err)
	}

	return q, nil
}
