package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
)

func main() {
	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if err := cfg.Validate(config.ServiceGateway); err != nil {
		slog.Error("invalid gateway configuration — refusing to start", "error", err)
		os.Exit(1)
	}

	// Initialize RabbitMQ Publisher with retry
	var pub *broker.Publisher
	for i := 0; i < 5; i++ {
		pub, err = broker.NewSafePublisher(cfg.RabbitMQ.URL, "telegram.inbound")
		if err == nil {
			break
		}
		slog.Warn("failed to initialize rabbitmq publisher, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("failed to initialize rabbitmq publisher after retries", "error", err)
		os.Exit(1)
	}
	defer pub.Close()

	// Initialize HTTP router
	r := setupRouter(pub, cfg)

	// Start server
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.GatewayPort),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("gateway starting", "port", cfg.Server.GatewayPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Register the Telegram webhook (with secret_token) in the background.
	// Network failures are retried with backoff and never crash the gateway.
	regCtx, cancelReg := context.WithCancel(context.Background())
	defer cancelReg()
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

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("gateway shutting down")
	cancelReg()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("failed to shutdown server gracefully", "error", err)
	}
}

type EventPublisher interface {
	Publish(ctx context.Context, msg any) error
}

func setupRouter(pub EventPublisher, cfg *config.Config) *chi.Mux {
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

		// Forward to orchestrator regardless of authorization
		// (Orchestrator handles unauthorized pairing flows)

		slog.Info("received message", "user_id", msg.UserID, "text", msg.Text)

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
