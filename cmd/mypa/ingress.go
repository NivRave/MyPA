package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/logging"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
)

// The ingress (formerly cmd/gateway, V4-01): it verifies and normalises the Telegram and Twilio
// webhooks and publishes each message to the durable inbound.messages queue. It never processes a
// message itself, so a webhook returns fast and a crash in a turn cannot lose an accepted message.
// It runs in the roles `all` and `ingress`.

// ingress is what the ingress side of the app holds while it runs.
type ingress struct {
	router   http.Handler
	pub      *broker.Publisher
	stopRegs context.CancelFunc
}

// startIngress connects the publisher (with retry, as the gateway did), builds the webhook router
// and registers the Telegram webhook in the background. Network failures of the registration are
// retried with backoff and never stop the app.
func startIngress(cfg *config.Config) (*ingress, error) {
	var pub *broker.Publisher
	var err error
	for i := 0; i < 5; i++ {
		pub, err = broker.NewSafePublisher(cfg.RabbitMQ.URL, broker.InboundMessagesQueue)
		if err == nil {
			break
		}
		slog.Warn("failed to initialize rabbitmq publisher, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("rabbitmq publisher: %w", err)
	}

	regCtx, cancelReg := context.WithCancel(context.Background())
	if cfg.Telegram.WebhookURL != "" {
		go func() {
			client := telegram.NewClient(cfg.Telegram.BotToken)
			err := registerWebhookWithRetry(regCtx, client, cfg.Telegram.WebhookURL, cfg.Telegram.SecretToken,
				webhookRegisterMaxAttempts, webhookRegisterBaseDelay)
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("giving up on telegram setWebhook; updates may be rejected until it is registered", "error", err)
			}
		}()
	} else {
		slog.Warn("TELEGRAM_WEBHOOK_URL not set; skipping setWebhook — the webhook must already be registered with the secret token")
	}

	return &ingress{router: setupIngressRouter(pub, cfg), pub: pub, stopRegs: cancelReg}, nil
}

// stop cancels a webhook registration still in progress and closes the publisher. Call it after
// the HTTP server has stopped accepting requests.
func (in *ingress) stop() {
	in.stopRegs()
	if err := in.pub.Close(); err != nil {
		slog.Warn("closing the inbound publisher failed", "error", err)
	}
}

type EventPublisher interface {
	Publish(ctx context.Context, msg any) error
}

func setupIngressRouter(pub EventPublisher, cfg *config.Config) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit for gateway webhooks
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	twHandler := twilio.NewHandler(pub, cfg.Twilio.AuthToken)
	r.Post("/webhook/twilio", twHandler.HandleWebhook)
	r.Post("/webhook/twilio/status", twHandler.HandleStatusCallback) // BL-8 delivery reports

	r.Post("/webhook/telegram", func(w http.ResponseWriter, r *http.Request) {
		if !telegramSecretValid(cfg.Telegram.SecretToken, r.Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
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

		msg, err := telegram.ParseUpdate(body)
		if err != nil {
			slog.Error("failed to parse telegram update", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}

		if msg == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Enqueue regardless of authorization
		// (the worker handles unauthorized pairing flows)

		// P3-3: never log message content at INFO (length + message_id only).
		logging.LogMessageText(r.Context(), nil, "received message", msg.Text, "user_id", msg.UserID, "message_id", msg.ID)

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := pub.Publish(ctx, *msg); err != nil {
			slog.Error("failed to publish message to broker", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	return r
}

// telegramSecretValid fails closed: an empty configured secret never matches.
// (subtle.ConstantTimeCompare("", "") returns 1, so the explicit empty check is required.)
func telegramSecretValid(configured, provided string) bool {
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

// webhookRegistrar is satisfied by *telegram.Client.
type webhookRegistrar interface {
	SetWebhook(ctx context.Context, webhookURL, secretToken string) error
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
		delay = min(delay*2, webhookRegisterMaxDelay)
	}
	return fmt.Errorf("telegram setWebhook failed after %d attempts: %w", maxAttempts, lastErr)
}
