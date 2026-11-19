package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests moved from internal/doctor with the check (V4-31).

var testNow = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return testNow }

var adminScope = doctor.Scope{Admin: &models.User{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"}}

// one runs a check and requires exactly one result.
func one(t *testing.T, c doctor.Check, scope doctor.Scope) doctor.Result {
	t.Helper()
	results := c.Run(context.Background(), scope)
	require.Len(t, results, 1, "%s", c.Name())
	return results[0]
}

// requireProblem asserts a result is a failure (or warning) that tells the admin what to do.
func requireProblem(t *testing.T, r doctor.Result, want doctor.Status, detailContains string) {
	t.Helper()
	assert.Equal(t, want, r.Status, "detail: %s", r.Detail)
	assert.Contains(t, r.Detail, detailContains)
	assert.NotEmpty(t, strings.TrimSpace(r.NextStep), "a problem must come with a next step")
}

func webhookCheck(info WebhookHealth, err error) WebhookCheck {
	return WebhookCheck{
		Configured: true,
		WantURL:    "https://mypa.example/webhook/telegram",
		Info:       func(context.Context) (WebhookHealth, error) { return info, err },
		Now:        fixedNow,
	}
}

func TestTelegramWebhook_Healthy(t *testing.T) {
	r := one(t, webhookCheck(WebhookHealth{URL: "https://mypa.example/webhook/telegram"}, nil), adminScope)
	assert.Equal(t, doctor.StatusOK, r.Status)
	assert.Empty(t, r.NextStep)
}

// V4-17 "done when": a broken webhook is detected. Each way it breaks is a failure with a next step.
func TestTelegramWebhook_Broken(t *testing.T) {
	t.Run("nothing registered", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookHealth{URL: ""}, nil), adminScope)
		requireProblem(t, r, doctor.StatusFail, "No webhook is registered")
	})
	t.Run("registered to another address", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookHealth{URL: "https://old-tunnel.example/webhook/telegram"}, nil), adminScope)
		requireProblem(t, r, doctor.StatusFail, "different address")
		assert.Contains(t, r.Detail, "old-tunnel.example")
	})
	t.Run("deliveries are failing", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookHealth{
			URL:          "https://mypa.example/webhook/telegram",
			Pending:      3,
			LastErrorAt:  testNow.Add(-12 * time.Minute),
			LastErrorMsg: "Connection refused",
		}, nil), adminScope)
		requireProblem(t, r, doctor.StatusFail, "Connection refused")
		assert.Contains(t, r.Detail, "12 minutes ago")
		assert.Contains(t, r.Detail, "3 updates are waiting")
	})
	t.Run("telegram cannot be asked", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookHealth{}, errors.New("Post: dial tcp: i/o timeout")), adminScope)
		requireProblem(t, r, doctor.StatusFail, "Could not ask Telegram")
	})
}

func TestTelegramWebhook_OldErrorWithNothingPendingIsAWarning(t *testing.T) {
	r := one(t, webhookCheck(WebhookHealth{
		URL:          "https://mypa.example/webhook/telegram",
		LastErrorAt:  testNow.Add(-5 * time.Hour),
		LastErrorMsg: "Read timeout expired",
	}, nil), adminScope)
	requireProblem(t, r, doctor.StatusWarn, "Read timeout expired")
}

func TestTelegramWebhook_ErrorOlderThanADayIsHistory(t *testing.T) {
	r := one(t, webhookCheck(WebhookHealth{
		URL:          "https://mypa.example/webhook/telegram",
		LastErrorAt:  testNow.Add(-72 * time.Hour),
		LastErrorMsg: "Connection refused",
	}, nil), adminScope)
	assert.Equal(t, doctor.StatusOK, r.Status)
}

func TestTelegramWebhook_NoConfiguredAddressSkipsTheComparison(t *testing.T) {
	c := webhookCheck(WebhookHealth{URL: "https://anything.example/webhook/telegram"}, nil)
	c.WantURL = ""
	assert.Equal(t, doctor.StatusOK, one(t, c, adminScope).Status)
}

func TestTelegramWebhook_NotConfigured(t *testing.T) {
	c := WebhookCheck{Configured: false, Info: func(context.Context) (WebhookHealth, error) {
		t.Fatal("Telegram must not be called when it is not configured")
		return WebhookHealth{}, nil
	}}
	assert.Equal(t, doctor.StatusInfo, one(t, c, adminScope).Status)
}

// V4-40: a polling install expects no webhook. Telegram holds the updates until the app fetches
// them, so a webhook registered by anything else means this install receives nothing; the likely
// cause is a second install live on the same bot.
func TestTelegramWebhook_Polling(t *testing.T) {
	polling := func(info WebhookHealth) WebhookCheck {
		c := webhookCheck(info, nil)
		c.Polling, c.WantURL = true, ""
		return c
	}
	t.Run("no webhook is healthy", func(t *testing.T) {
		r := one(t, polling(WebhookHealth{Pending: 2}), adminScope)
		assert.Equal(t, doctor.StatusOK, r.Status)
		assert.Contains(t, r.Detail, "polls")
		assert.Contains(t, r.Detail, "2 updates are waiting")
		assert.Empty(t, r.NextStep)
	})
	t.Run("a webhook set by someone else", func(t *testing.T) {
		r := one(t, polling(WebhookHealth{URL: "https://old-install.example/webhook/telegram"}), adminScope)
		requireProblem(t, r, doctor.StatusFail, "old-install.example")
		assert.Contains(t, r.NextStep, "only one install")
	})
}

// The next steps name the setting that exists now: PUBLIC_URL replaced TELEGRAM_WEBHOOK_URL.
func TestTelegramWebhook_NextStepsNameThePublicAddressSetting(t *testing.T) {
	for _, info := range []WebhookHealth{
		{URL: ""},
		{URL: "https://old-tunnel.example/webhook/telegram"},
		{URL: "https://mypa.example/webhook/telegram", Pending: 1, LastErrorAt: testNow.Add(-time.Minute), LastErrorMsg: "x"},
	} {
		r := one(t, webhookCheck(info, nil), adminScope)
		assert.Contains(t, r.NextStep, "PUBLIC_URL", "%+v", info)
		assert.NotContains(t, r.NextStep, "TELEGRAM_WEBHOOK_URL")
	}
}
