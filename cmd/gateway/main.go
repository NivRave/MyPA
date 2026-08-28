package main

import (
	"context"
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

	// Initialize RabbitMQ Publisher with retry
	var pub *broker.Publisher
	for i := 0; i < 5; i++ {
		pub, err = broker.NewPublisher(cfg.RabbitMQ.URL, "telegram.inbound")
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
		Addr:    fmt.Sprintf(":%d", cfg.Server.GatewayPort),
		Handler: r,
	}

	go func() {
		slog.Info("gateway starting", "port", cfg.Server.GatewayPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("gateway shutting down")
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

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	allowedUsers := cfg.ParseAllowedUsers()

	twHandler := twilio.NewHandler(pub, allowedUsers)
	r.Post("/webhook/twilio", twHandler.HandleWebhook)

	r.Post("/webhook/telegram", func(w http.ResponseWriter, r *http.Request) {
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

		// Verify user
		if _, allowed := allowedUsers[msg.UserID]; len(allowedUsers) > 0 && !allowed {
			slog.Warn("unauthorized telegram user", "user_id", msg.UserID)
			w.WriteHeader(http.StatusOK) // Return 200 so Telegram stops retrying
			return
		}

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
