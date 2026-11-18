package broker

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNonRetryable_WrapsAndIsDetectable(t *testing.T) {
	base := errors.New("send failed")
	wrapped := NonRetryable(base)
	assert.ErrorIs(t, wrapped, ErrNonRetryable)
	assert.ErrorIs(t, wrapped, base, "original cause must stay inspectable")
	assert.Same(t, wrapped, NonRetryable(wrapped), "double wrapping is a no-op")
	assert.Nil(t, NonRetryable(nil))

	// Survives further wrapping by callers
	assert.ErrorIs(t, fmt.Errorf("process: %w", wrapped), ErrNonRetryable)
}

func TestDecideFailure(t *testing.T) {
	retryable := errors.New("llm timeout")
	cases := []struct {
		name    string
		err     error
		retries int // prior failures recorded in headers
		max     int
		want    failureAction
	}{
		{"FirstFailureRetryable_Requeue", retryable, 0, 3, actionRequeue},
		{"SecondFailureRetryable_Requeue", retryable, 1, 3, actionRequeue},
		{"FinalAttemptRetryable_DeadLetter", retryable, 2, 3, actionDeadLetter},
		{"NonRetryableFirstAttempt_Ack", NonRetryable(retryable), 0, 3, actionAck},
		{"NonRetryableFinalAttempt_Ack", NonRetryable(retryable), 2, 3, actionAck},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideFailure(tc.err, tc.retries, tc.max))
		})
	}
}

func TestDeliveryInfo_IsFinal(t *testing.T) {
	assert.False(t, DeliveryInfo{Attempt: 1, MaxAttempts: 3}.IsFinal())
	assert.False(t, DeliveryInfo{Attempt: 2, MaxAttempts: 3}.IsFinal())
	assert.True(t, DeliveryInfo{Attempt: 3, MaxAttempts: 3}.IsFinal())
	assert.True(t, DeliveryInfo{Attempt: 1, MaxAttempts: 1}.IsFinal())
}

func TestDeliveryInfoFromRetries(t *testing.T) {
	assert.Equal(t, DeliveryInfo{Attempt: 1, MaxAttempts: 3}, deliveryInfo(0, 3))
	assert.Equal(t, DeliveryInfo{Attempt: 3, MaxAttempts: 3}, deliveryInfo(2, 3))
}
