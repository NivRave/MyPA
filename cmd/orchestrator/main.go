package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/nivik/mypa/internal/logging"
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
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/workspace_calendar"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
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
	migrateOnly := flag.Bool("migrate-only", false, "apply database schema migrations and exit (P4-3)")
	flag.Parse()

	// Initialize logger
	logging.Setup() // JSON on stdout at LOG_LEVEL (default info); P3-3

	// Load config
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// P4-3: schema changes are an explicit step (the `migrate` compose service runs this).
	if *migrateOnly {
		os.Exit(runMigrations(cfg.Database.URL))
	}

	if err := cfg.Validate(config.ServiceOrchestrator); err != nil {
		slog.Error("invalid orchestrator configuration — refusing to start", "error", err)
		os.Exit(1)
	}
	authCfg = cfg.Server

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
		// Parallel across users, ordered per user; RABBITMQ_PREFETCH bounds concurrent turns (P2-8).
		consumer, err = broker.NewConsumerWithOptions(cfg.RabbitMQ.URL, "telegram.inbound", broker.WithPrefetch(cfg.RabbitMQ.Prefetch))
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
		dbClient, err = db.Open(dbURL)
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
	// P4-3: never run against a schema with pending migrations.
	if sqlDB, sqlErr := dbClient.DB.DB(); sqlErr != nil {
		slog.Error("failed to access database handle", "error", sqlErr)
		os.Exit(1)
	} else if err := db.EnsureMigrated(ctx, sqlDB); err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}

	// 8. Initialize Twilio Client
	twilioClient := twilio.NewClient(cfg.Twilio)

	gmailClient := gmail.NewClient(oauthCfg, store)
	tasksClient := tasks.NewClient(oauthCfg, store)
	contactsClient := contacts.NewClient(oauthCfg, store)
	tavilyClient := tavily.NewClient(cfg.Tavily.APIKey)

	// 8.5 Initialize Telemetry Publisher
	telemetryPublisher, err := broker.NewSafePublisher(cfg.RabbitMQ.URL, "audit.events")
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

	// 8.6 Initialize Wave 1 EventBus with dual-lane worker pool and Domain ToolModules
	eventBus := events.NewEventBus(events.WithWorkerCount(4), events.WithQueueCapacity(1024))
	registry := orchestrator.NewRegistry()
	if store != nil {
		orchestrator.RegisterPromptInvalidator(eventBus, store)
		registry.WithPromptCache(store)
	}

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

	// Register all Wave 0, Wave 1, and Wave 2 domain modules in the ToolRegistry
	workspaceCalendarModule := workspace_calendar.NewModule(calendarFactory, workspace_calendar.WithEventBus(eventBus))
	workspaceGmailModule := workspace_gmail.NewModule(workspace_gmail.WithClient(gmailClient), workspace_gmail.WithEventBus(eventBus))
	workspaceTasksModule := workspace_tasks.NewModule(workspace_tasks.WithClient(tasksClient), workspace_tasks.WithEventBus(eventBus))
	workspaceContactsModule := workspace_contacts.NewModule(workspace_contacts.WithClient(contactsClient), workspace_contacts.WithEventBus(eventBus))
	webResearchModule := web_research.NewModule(web_research.WithClient(tavilyClient))

	registry.Register(workspaceCalendarModule)
	registry.Register(workspaceGmailModule)
	registry.Register(workspaceTasksModule)
	registry.Register(workspaceContactsModule)
	registry.Register(webResearchModule)

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
		WithEventBus(eventBus).
		WithCalendarConventions(orchestrator.ParseCalendarConventions(cfg.Server.WeekStartDay, cfg.Server.WeekendDays))

	// Start Cron jobs
	c := scheduler.StartCronJobs(engine, dbClient, automationRunner, cfg.Server.DefaultTimezone)
	defer c.Stop()

	engine.SetWorkflowsChangedCallback(func() {
		scheduler.ReloadWorkflows(engine, dbClient, automationRunner, cfg.Server.DefaultTimezone)
	})

	// Run engine in a goroutine
	go func() {
		slog.Info("orchestrator engine starting...")
		if err := engine.Start(ctx); err != nil {
			slog.Error("engine stopped with error", "error", err)
		}
	}()

	// 7. Start HTTP Server for OAuth callbacks and GraphQL
	mux := setupRouter(oauthCfg, store, tgClient, dbClient, durabilityModule, eventBus)

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

	// 1. Stop HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)

	// 2. Drain and close EventBus worker pool
	eventBusShutdownCtx, eventBusCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer eventBusCancel()
	if err := eventBus.Close(eventBusShutdownCtx); err != nil {
		slog.Error("failed to gracefully drain event bus", "error", err)
	}

	// 3. Wait for engine background tasks
	engine.Wait()

	// 4. Close database connection pool
	if dbClient != nil {
		if err := dbClient.Close(); err != nil {
			slog.Error("failed to close db connection pool", "error", err)
		}
	}

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

// runMigrations applies all pending schema migrations and returns the process exit code.
func runMigrations(databaseURL string) int {
	if strings.TrimSpace(databaseURL) == "" {
		slog.Error("DATABASE_URL is required for -migrate-only")
		return 1
	}
	var client *db.Client
	var err error
	for i := 0; i < 10; i++ { // the database may still be starting
		if client, err = db.Open(databaseURL); err == nil {
			break
		}
		slog.Warn("migrate: database not ready, retrying", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("migrate: cannot connect to database", "error", err)
		return 1
	}
	defer client.Close()
	sqlDB, err := client.DB.DB()
	if err != nil {
		slog.Error("migrate: database handle", "error", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		slog.Error("migrate: failed", "error", err)
		return 1
	}
	slog.Info("migrate: schema is up to date")
	return 0
}

// authCfg holds the validated /graphql auth settings (set in main). Zero value = fail closed:
// no dashboard secret or JWT is accepted (e.g. in tests that build the router directly).
var authCfg config.ServerConfig

func setupRouter(oauthCfg *calendar.OAuthConfig, store *state.Store, tgClient AuthTelegramClient, dbClient *db.Client, fallbackSaver CredentialFallbackSaver, eventBus ...*events.EventBus) *http.ServeMux {
	mux := http.NewServeMux()

	// GraphQL Server
	if dbClient != nil {
		var durEngine durability.RollbackEngine
		if dMod, ok := fallbackSaver.(durability.RollbackEngine); ok {
			durEngine = dMod
		}
		var bus *events.EventBus
		if len(eventBus) > 0 {
			bus = eventBus[0]
		}
		resolvers := graph.NewResolver(graph.ResolverConfig{
			DB:             dbClient,
			EventBus:       bus,
			Durability:     durEngine,
			BabyRepo:       db.NewBabyRepository(dbClient.DB),
			HouseRepo:      db.NewHouseRepository(dbClient.DB),
			FinanceRepo:    db.NewFinanceRepository(dbClient.DB),
			ScheduleRepo:   db.NewScheduleRepository(dbClient.DB),
			SharedRepo:     db.NewSharedRepository(dbClient.DB),
			MemoryRepo:     db.NewMemoryV2Repo(dbClient.DB),
			AutomationRepo: db.NewAutomationRepository(dbClient.DB),
		})
		srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: resolvers}))

		// P3-2: secrets come from validated config (cfg.Validate requires JWT_SECRET, distinct from
		// ENCRYPTION_KEY — no fallback), and the dashboard secret is bound to one family group.
		authMiddleware := graph.NewAuthMiddleware(graph.AuthConfig{
			DashboardSecret:      authCfg.DashboardAPISecret,
			DashboardFamilyGroup: authCfg.DashboardFamilyGroup,
			JWTSecret:            authCfg.JWTSecret,
		})

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
