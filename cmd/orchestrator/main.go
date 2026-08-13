package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nivik/mypa/internal/audio"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/state"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Redis Store
	redisURL := cfg.Redis.URL
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	store, err := state.NewStore(redisURL)
	if err != nil {
		slog.Error("failed to initialize redis store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// 2. Initialize RabbitMQ Consumer
	amqpURL := cfg.RabbitMQ.URL
	if amqpURL == "" {
		amqpURL = "amqp://mypa:mypa_dev@localhost:5672/"
	}
	consumer, err := broker.NewConsumer(amqpURL, "telegram.inbound")
	if err != nil {
		slog.Error("failed to initialize rabbitmq consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	// 3. Initialize Telegram Client
	tgClient := telegram.NewClient(cfg.Telegram.BotToken)

	// 4. Initialize Gemini LLM Client
	llmClient, err := llm.NewClient(ctx, cfg.Gemini.APIKey, cfg.Gemini.Model)
	if err != nil {
		slog.Error("failed to initialize gemini client", "error", err)
		os.Exit(1)
	}

	// 5. Initialize OAuth Config
	oauthCfg := calendar.NewOAuthConfig(&cfg.Google)

	// 6. Initialize Groq Audio Client
	audioClient := audio.NewClient(cfg.Groq.APIKey)

	// 7. Initialize Engine
	engine := orchestrator.NewEngine(consumer, store, llmClient, tgClient, oauthCfg, audioClient, cfg.Server.DefaultTimezone)

	// Run engine in a goroutine
	go func() {
		slog.Info("orchestrator engine starting...")
		if err := engine.Start(ctx); err != nil {
			slog.Error("engine stopped with error", "error", err)
		}
	}()

	// 7. Start HTTP Server for OAuth callbacks
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		stateParam := r.URL.Query().Get("state") // This is the Telegram UserID

		if code == "" || stateParam == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		// Exchange code for token
		token, err := oauthCfg.Exchange(r.Context(), code)
		if err != nil {
			slog.Error("failed to exchange oauth code", "error", err)
			http.Error(w, "failed to exchange token", http.StatusInternalServerError)
			return
		}

		// Encode and save to Redis
		tokenBytes, err := calendar.EncodeToken(token)
		if err != nil {
			slog.Error("failed to encode token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if err := store.SetOAuthToken(r.Context(), stateParam, tokenBytes); err != nil {
			slog.Error("failed to save token to redis", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Notify user on Telegram
		_ = tgClient.SendMessage(r.Context(), stateParam, "✅ Successfully connected to Google Calendar! You can now ask me to schedule events.")

		// Tell the browser they can close the window
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<h1>Success!</h1><p>Google Calendar connected. You can close this window and return to Telegram.</p>"))
	})

	serverAddr := fmt.Sprintf(":%d", cfg.Server.OrchestratorPort)
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}

	go func() {
		slog.Info("orchestrator http server starting", "port", cfg.Server.OrchestratorPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server failed", "error", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("orchestrator shutting down")
	cancel()
	_ = srv.Shutdown(context.Background())

	// Give components a moment to finish current processing
	time.Sleep(1 * time.Second)
}
