package broker

import (
	"errors"
	"fmt"
)

// ErrNonRetryable marks a handler failure that must NOT be redelivered: the handler already
// produced external side effects (tool calls, a reply sent), so re-running it would duplicate
// them (calendar events, emails, ledger rows). The consumer acks such deliveries.
var ErrNonRetryable = errors.New("non-retryable handler failure")

// NonRetryable wraps err so errors.Is(err, ErrNonRetryable) holds; nil and already-wrapped
// errors are returned unchanged. The original cause remains inspectable via errors.Is/As.
func NonRetryable(err error) error {
	if err == nil || errors.Is(err, ErrNonRetryable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrNonRetryable, err)
}

// DeliveryInfo describes which delivery attempt a handler is processing (1-based).
type DeliveryInfo struct {
	Attempt     int
	MaxAttempts int
}

// IsFinal reports whether a retryable failure on this attempt will dead-letter the message.
func (d DeliveryInfo) IsFinal() bool { return d.Attempt >= d.MaxAttempts }

func deliveryInfo(priorRetries, maxRetries int) DeliveryInfo {
	return DeliveryInfo{Attempt: priorRetries + 1, MaxAttempts: maxRetries}
}

type failureAction int

const (
	actionRequeue    failureAction = iota // republish with incremented retry header
	actionDeadLetter                      // reject without requeue → DLX → <queue>.dlq
	actionAck                             // drop: retrying would duplicate side effects
)

// decideFailure maps a handler error and the delivery's prior retry count to an action.
func decideFailure(err error, priorRetries, maxRetries int) failureAction {
	if errors.Is(err, ErrNonRetryable) {
		return actionAck
	}
	if deliveryInfo(priorRetries, maxRetries).IsFinal() {
		return actionDeadLetter
	}
	return actionRequeue
}
