package telegram

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/logging"
)

// WebhookPath is where Telegram delivers updates.
const WebhookPath = channel.WebhookPrefix + "telegram"

// secretHeader carries the secret token Telegram was given at setWebhook.
const secretHeader = "X-Telegram-Bot-Api-Secret-Token"

// webhookHandler verifies an update, normalises it and hands it to the sink. Moved from the
// ingress in cmd/mypa (V4-31) with the same answers: 401 without the right secret token, 400 for
// an unreadable body, 200 for an update that carries nothing for the assistant, 500 when the
// message could not be queued (Telegram then retries).
func webhookHandler(secret string, sink channel.Sink) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !secretValid(secret, r.Header.Get(secretHeader)) {
			slog.Warn("unauthorized telegram webhook attempt", "remote_addr", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("failed to read request body", "error", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		msg, err := ParseUpdate(body)
		if err != nil {
			slog.Error("failed to parse telegram update", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if msg == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Enqueued regardless of authorization: the worker handles unauthorized pairing flows.
		// P3-3: never log message content at INFO (length + message_id only).
		logging.LogMessageText(r.Context(), nil, "received message", msg.Text, "user_id", msg.Sender, "message_id", msg.MessageID)

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := sink(ctx, *msg); err != nil {
			slog.Error("failed to publish message to broker", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// secretValid fails closed: an empty configured secret never matches.
// (subtle.ConstantTimeCompare("", "") returns 1, so the explicit empty check is required.)
func secretValid(configured, provided string) bool {
	if configured == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(configured)) == 1
}

const (
	webhookRegisterMaxAttempts = 8
	webhookRegisterBaseDelay   = 2 * time.Second
	webhookRegisterMaxDelay    = 2 * time.Minute
)

// webhookRegistrar is satisfied by *Client.
type webhookRegistrar interface {
	SetWebhook(ctx context.Context, webhookURL, secretToken string) error
}

// registerWebhook tells Telegram where to deliver, in the background of the ingress. Network
// failures are retried with backoff and never stop the app.
func registerWebhook(ctx context.Context, reg webhookRegistrar, cfg Config) {
	if cfg.WebhookURL == "" {
		slog.Warn("TELEGRAM_WEBHOOK_URL not set; skipping setWebhook — the webhook must already be registered with the secret token")
		return
	}
	err := registerWebhookWithRetry(ctx, reg, cfg.WebhookURL, cfg.SecretToken, webhookRegisterMaxAttempts, webhookRegisterBaseDelay)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("giving up on telegram setWebhook; updates may be rejected until it is registered", "error", err)
	}
}

// registerWebhookWithRetry calls SetWebhook until it succeeds, maxAttempts is reached, or ctx is
// cancelled. Delays grow exponentially from baseDelay, capped at webhookRegisterMaxDelay.
func registerWebhookWithRetry(ctx context.Context, reg webhookRegistrar, webhookURL, secretToken string, maxAttempts int, baseDelay time.Duration) error {
	delay := baseDelay
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = reg.SetWebhook(ctx, webhookURL, secretToken)
		if lastErr == nil {
			slog.Info("telegram webhook registered with secret token", "url", webhookURL, "attempt", attempt)
			return nil
		}
		slog.Warn("telegram setWebhook failed", "attempt", attempt, "max_attempts", maxAttempts, "error", lastErr)
		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2 // the package's own min() is for ints, so the cap is spelled out
		if delay > webhookRegisterMaxDelay {
			delay = webhookRegisterMaxDelay
		}
	}
	return fmt.Errorf("telegram setWebhook failed after %d attempts: %w", maxAttempts, lastErr)
}
