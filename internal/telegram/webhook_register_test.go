package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTelegramSecret = "test-telegram-secret_ABC-123"

// setWebhook registration with retry/backoff. Moved from cmd/mypa with the function (V4-31).

type fakeRegistrar struct {
	mu        sync.Mutex
	failFirst int
	calls     int
	gotURL    string
	gotSecret string
}

func (f *fakeRegistrar) SetWebhook(_ context.Context, webhookURL, secretToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotURL, f.gotSecret = webhookURL, secretToken
	if f.calls <= f.failFirst {
		return errors.New("network down")
	}
	return nil
}

func TestRegisterWebhookWithRetry_SucceedsAfterFailures(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 2}
	err := registerWebhookWithRetry(context.Background(), reg, "https://x.example/webhook/telegram", testTelegramSecret, 5, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, 3, reg.calls)
	assert.Equal(t, "https://x.example/webhook/telegram", reg.gotURL)
	assert.Equal(t, testTelegramSecret, reg.gotSecret)
}

func TestRegisterWebhookWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 100}
	err := registerWebhookWithRetry(context.Background(), reg, "https://x.example/webhook/telegram", testTelegramSecret, 3, time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, 3, reg.calls)
}

func TestRegisterWebhookWithRetry_StopsOnContextCancel(t *testing.T) {
	reg := &fakeRegistrar{failFirst: 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := registerWebhookWithRetry(ctx, reg, "https://x.example/webhook/telegram", testTelegramSecret, 10, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	assert.LessOrEqual(t, reg.calls, 1)
}
