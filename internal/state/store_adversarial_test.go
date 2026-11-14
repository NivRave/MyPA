package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_RateLimit_LockoutExpiry tests that the 1-hour lockout
// strictly expires after 1 hour, allowing the user to attempt again.
func TestAdversarial_RateLimit_LockoutExpiry(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	userID := "adv-user-lockout"

	// 1-5 failed attempts trigger lockout
	for i := 1; i <= 5; i++ {
		_, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
		require.NoError(t, err)
		if i == 5 {
			assert.True(t, locked, "5th attempt must lock out")
		} else {
			assert.False(t, locked)
		}
	}

	// Verify locked out
	locked, ttl, err := store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.True(t, ttl > 0)

	// Fast-forward 30 minutes: still locked out
	mr.FastForward(30 * time.Minute)
	locked, ttl, err = store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.True(t, locked)
	assert.True(t, ttl <= 30*time.Minute)

	// Fast-forward another 31 minutes (total 61 mins > 1 hour): lockout MUST expire
	mr.FastForward(31 * time.Minute)
	locked, _, err = store.CheckPairingLockout(ctx, userID)
	require.NoError(t, err)
	assert.False(t, locked, "Lockout must expire after 1 hour")

	// Next attempt must start fresh (attempt 1)
	attempts, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), attempts, "First attempt after lockout expiry must reset attempt count")
	assert.False(t, locked)
}

// TestAdversarial_RateLimit_ResetOnSuccess tests that successful redemption
// completely wipes failed attempt counters and active lockouts.
func TestAdversarial_RateLimit_ResetOnSuccess(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	userID := "adv-user-reset"

	// 3 failed attempts
	for i := 1; i <= 3; i++ {
		attempts, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, int64(i), attempts)
		assert.False(t, locked)
	}

	// Reset on success
	err = store.ResetPairingRateLimit(ctx, userID)
	require.NoError(t, err)

	// Check keys are deleted
	assert.False(t, mr.Exists("rate_limit:pairing:"+userID))
	assert.False(t, mr.Exists("rate_limit:pairing:lockout:"+userID))

	// Next attempt must start at 1, not 4
	attempts, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), attempts, "Attempt after reset must be 1")
	assert.False(t, locked)
}

// TestAdversarial_RateLimit_WindowExpiryWithoutLockout tests that if 15 minutes pass
// with fewer than 5 failures, the window expires and counter resets.
func TestAdversarial_RateLimit_WindowExpiryWithoutLockout(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	userID := "adv-user-window"

	// 4 failed attempts
	for i := 1; i <= 4; i++ {
		attempts, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, int64(i), attempts)
		assert.False(t, locked)
	}

	// Fast-forward 16 minutes (> 15-minute rolling window)
	mr.FastForward(16 * time.Minute)

	// Counter must have expired
	assert.False(t, mr.Exists("rate_limit:pairing:"+userID))

	// Next failure should be attempt 1, NOT attempt 5 (no lockout!)
	attempts, locked, err := store.RecordFailedPairingAttempt(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), attempts, "After 15m window expiry, attempt counter must restart at 1")
	assert.False(t, locked, "Must NOT be locked out on the 1st attempt of a new window")
}
