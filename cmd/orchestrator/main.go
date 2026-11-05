package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"golang.org/x/oauth2"

	"github.com/nivik/mypa/internal/audio"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/contacts"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/domains/house"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/gmail"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/scheduler"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/tasks"
	"github.com/nivik/mypa/internal/tavily"
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

	if err := crypto.ValidateKeyConfigured(); err != nil {
		slog.Error("encryption key validation failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 4. Initialize Redis
	var store *state.Store
	for i := 0; i < 5; i++ {
		store, err = state.NewStore(cfg.Redis.URL)
		if err == nil {
			break
		}
		slog.Warn("failed to initialize redis store, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("failed to initialize redis store after retries", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// 5. Initialize RabbitMQ Consumer
	var consumer *broker.Consumer
	for i := 0; i < 5; i++ {
		consumer, err = broker.NewConsumer(cfg.RabbitMQ.URL, "telegram.inbound")
		if err == nil {
			break
		}
		slog.Warn("failed to initialize rabbitmq consumer, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("failed to initialize rabbitmq consumer after retries", "error", err)
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

	// 7. Initialize Database Client
	dbURL := cfg.Database.URL
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	db.RegisterWave1Models()
	db.RegisterWave2Models()
	var dbClient *db.Client
	for i := 0; i < 5; i++ {
		dbClient, err = db.NewClient(dbURL)
		if err == nil {
			break
		}
		slog.Warn("failed to initialize database, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("failed to initialize database after retries", "error", err)
		os.Exit(1)
	}

	// 8. Initialize Twilio Client
	twilioClient := twilio.NewClient(cfg.Twilio)

	gmailClient := gmail.NewClient(oauthCfg, store)
	tasksClient := tasks.NewClient(oauthCfg, store)
	contactsClient := contacts.NewClient(oauthCfg, store)
	tavilyClient := tavily.NewClient(cfg.Tavily.APIKey)

	// 8.5 Initialize Telemetry Publisher
	telemetryPublisher, err := broker.NewPublisher(cfg.RabbitMQ.URL, "audit.events")
	if err != nil {
		slog.Error("failed to initialize telemetry publisher", "error", err)
		os.Exit(1)
	}
	defer telemetryPublisher.Close()

	// Bootstrap legacy ALLOWED_USERS into Database
	allowedMap := cfg.ParseAllowedUsers()
	for _, u := range allowedMap {
		if err := dbClient.UpsertUser(u); err != nil {
			slog.Error("failed to bootstrap allowed user", "error", err)
		}
	}

	// 8.6 Initialize Wave 1 EventBus and Domain ToolModules
	eventBus := events.NewEventBus()
	registry := orchestrator.NewRegistry()

	// Track 1A: Durability Module (audit logs, undo, data export)
	durabilityModule := durability.NewModule(dbClient, eventBus)
	if store != nil {
		store.SetCredentialFallback(func(fallbackCtx context.Context, userID string) ([]byte, error) {
			return durabilityModule.GetCredentialFallback(fallbackCtx, userID, "google_oauth")
		})
	}

	// Track 1B: Topic Memory Module (topic memory, entity profiles)
	memoryRepo := db.NewMemoryV2Repo(dbClient.DB)
	memoryModule := memory.NewModule(memoryRepo)

	// Proactive Notification Sender for Shared Spaces and Automations
	notificationSender := func(notifyCtx context.Context, recipientID, text string) error {
		if strings.HasPrefix(recipientID, "+") || strings.HasPrefix(recipientID, "whatsapp:") {
			if twilioClient != nil {
				return twilioClient.SendMessage(notifyCtx, recipientID, text)
			}
		}
		if tgClient != nil {
			return tgClient.SendMessage(notifyCtx, recipientID, text)
		}
		return nil
	}

	// Track 1C: Shared Spaces & Partner Sync Notifier
	sharedNotifier := shared.NewNotifier(dbClient, notificationSender)
	sharedNotifier.RegisterWithBus(eventBus)
	sharedModule := shared.NewModule(dbClient, eventBus, sharedNotifier)

	// Track 1D: Multi-Schedule & Virtual Sub-Calendars
	calendarFactory := func(calCtx context.Context, userID string) (orchestrator.CalendarClient, error) {
		tokenBytes, err := store.GetOAuthToken(calCtx, userID)
		if err != nil || tokenBytes == nil {
			return nil, fmt.Errorf("unauthorized")
		}
		var tok oauth2.Token
		if err := json.Unmarshal(tokenBytes, &tok); err != nil {
			return nil, fmt.Errorf("unauthorized")
		}
		ts := oauthCfg.TokenSource(calCtx, &tok)
		return calendar.NewClient(calCtx, ts)
	}
	scheduleModule := schedule.NewModule(dbClient, schedule.WithCalendarFactory(calendarFactory), schedule.WithEventBus(eventBus))

	// Track 2E: Baby & Family Care Tracker
	babyModule := baby.NewModule(dbClient, baby.WithEventBus(eventBus))

	// Track 2F: House Maintenance, Contracts & Vehicle Vault
	houseModule := house.NewModule(dbClient, house.WithEventBus(eventBus))

	// Track 2G: Financial Tracking & Budget Intelligence
	financeModule := finance.NewModule(dbClient, finance.WithEventBus(eventBus))

	// Track 2H: On-Demand External APIs, Automations & Personal CRM
	automationRunner := automation.NewRunner(dbClient, eventBus, automation.WithNotificationSender(notificationSender))
	automationModule := automation.NewModule(dbClient, automation.WithEventBus(eventBus), automation.WithRunner(automationRunner))

	// Register all Wave 1 and Wave 2 domain modules in the ToolRegistry
	registry.Register(durabilityModule)
	registry.Register(memoryModule)
	registry.Register(sharedModule)
	registry.Register(scheduleModule)
	registry.Register(babyModule)
	registry.Register(houseModule)
	registry.Register(financeModule)
	registry.Register(automationModule)

	// Wire registry declarations into llmClient
	llmClient.AddFunctionDeclarations(registry.AllDeclarations()...)

	// 9. Initialize Engine with ToolRegistry and EventBus
	engine := orchestrator.NewEngine(consumer, store, dbClient, llmClient, tgClient, twilioClient, oauthCfg, gmailClient, tasksClient, contactsClient, tavilyClient, audioClient, telemetryPublisher, cfg.Server.DefaultTimezone).
		WithToolRegistry(registry).
		WithEventBus(eventBus)

	// Start Cron jobs
	c := scheduler.StartCronJobs(engine, dbClient)
	defer c.Stop()
	
	engine.SetWorkflowsChangedCallback(func() {
		scheduler.ReloadWorkflows(engine, dbClient)
	})

	// Run engine in a goroutine
	go func() {
		slog.Info("orchestrator engine starting...")
		if err := engine.Start(ctx); err != nil {
			slog.Error("engine stopped with error", "error", err)
		}
	}()

	// 7. Start HTTP Server for OAuth callbacks and GraphQL
	mux := setupRouter(oauthCfg, store, tgClient, dbClient, durabilityModule)

	serverAddr := fmt.Sprintf(":%d", cfg.Server.OrchestratorPort)
	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
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

	// Wait for engine background tasks
	engine.Wait()
	slog.Info("orchestrator shutdown complete")
}

type AuthTelegramClient interface {
	SendMessage(ctx context.Context, chatID string, text string) error
}

type CredentialFallbackSaver interface {
	SaveCredentialFallback(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error
}

func setupAuthRouter(oauthCfg *calendar.OAuthConfig, store *state.Store, tgClient AuthTelegramClient, fallbackSaver ...CredentialFallbackSaver) *http.ServeMux {
	var saver CredentialFallbackSaver
	if len(fallbackSaver) > 0 {
		saver = fallbackSaver[0]
	}
	return setupRouter(oauthCfg, store, tgClient, nil, saver)
}

func setupRouter(oauthCfg *calendar.OAuthConfig, store *state.Store, tgClient AuthTelegramClient, dbClient *db.Client, fallbackSaver CredentialFallbackSaver) *http.ServeMux {
	mux := http.NewServeMux()
	
	// GraphQL Server
	if dbClient != nil {
		srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: dbClient}}))

		dashboardSecret := os.Getenv("DASHBOARD_API_SECRET")
		jwtSecret := os.Getenv("JWT_SECRET")
		if jwtSecret == "" {
			jwtSecret = os.Getenv("ENCRYPTION_KEY")
		}
		authMiddleware := graph.NewAuthMiddleware(dashboardSecret, jwtSecret)

		graphqlHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit for GraphQL
			srv.ServeHTTP(w, r)
		})
		mux.Handle("/graphql", authMiddleware(graphqlHandler))

		if os.Getenv("ENV") != "production" {
			mux.Handle("/playground", playground.Handler("GraphQL playground", "/graphql"))
		} else {
			mux.HandleFunc("/playground", func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			})
		}
	}

	mux.HandleFunc("/auth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		stateParam := r.URL.Query().Get("state")

		if code == "" || stateParam == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		targetUserID := stateParam
		if store != nil {
			validatedUser, err := store.ValidateAndDeleteOAuthState(r.Context(), stateParam)
			if err != nil || validatedUser == "" {
				slog.Warn("invalid or expired oauth state nonce", "state", stateParam, "error", err)
				http.Error(w, "invalid or expired oauth state nonce", http.StatusForbidden)
				return
			}
			targetUserID = validatedUser
		}

		// Exchange code for token
		token, err := oauthCfg.Exchange(r.Context(), code)
		if err != nil {
			slog.Error("failed to exchange oauth code", "error", err)
			http.Error(w, "failed to exchange token", http.StatusInternalServerError)
			return
		}

		// Encode and save to Redis (AES-256-GCM encrypted via SetOAuthToken)
		tokenBytes, err := calendar.EncodeToken(token)
		if err != nil {
			slog.Error("failed to encode token", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if store != nil {
			if err := store.SetOAuthToken(r.Context(), targetUserID, tokenBytes); err != nil {
				slog.Error("failed to save token to redis", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		// Persist credential fallback if configured
		if fallbackSaver != nil {
			if err := fallbackSaver.SaveCredentialFallback(r.Context(), targetUserID, "google_oauth", tokenBytes); err != nil {
				slog.Error("failed to persist credential fallback", "user_id", targetUserID, "error", err)
			} else {
				slog.Info("persisted encrypted credential fallback", "user_id", targetUserID)
			}
		}

		// Notify user on Telegram
		if tgClient != nil {
			_ = tgClient.SendMessage(r.Context(), targetUserID, "✅ Successfully connected to Google Calendar! You can now ask me to schedule events.")
		}

		// Tell the browser they can close the window
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Success!</h1><p>Google Calendar connected. You can close this window and return to Telegram.</p>"))
	})
	return mux
}
