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
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Webhook endpoint
	r.Post("/webhook/telegram", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("failed to read request body", "error", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Parse the Telegram update
		msg, err := telegram.ParseUpdate(body)
		if err != nil {
			slog.Error("failed to parse telegram update", "error", err)
			// Return 200 so Telegram doesn't keep retrying bad payloads
			w.WriteHeader(http.StatusOK)
			return
		}

		if msg == nil {
			// Not a text message, ignore
			w.WriteHeader(http.StatusOK)
			return
		}

		slog.Info("received message", "user_id", msg.UserID, "text", msg.Text)

		// Publish to RabbitMQ
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := pub.Publish(ctx, *msg); err != nil {
			slog.Error("failed to publish message to broker", "error", err)
			// Return 500 so Telegram retries later
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

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
