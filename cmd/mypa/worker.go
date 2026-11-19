package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/nivik/mypa/internal/audio"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/channel"
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
)

// The worker (formerly the orchestrator's main, V4-01): it consumes inbound.messages, runs the
// conversation turns with their tools, the event bus and the scheduler, and serves /graphql and the
// Google sign-in callback. It runs in the roles `all` and `worker`, always as a single replica: the
// scheduler has no leader election and the per-user ordering is in-process (ADR 0001).

// worker is what the worker side of the app holds while it runs.
type worker struct {
	router  http.Handler
	cancel  context.CancelFunc
	engine  *orchestrator.Engine
	bus     *events.EventBus
	cleanup []func() // run in reverse order by stop
}

// startWorker connects every dependency, starts the engine and the scheduler, and returns the app
// router. On an error, whatever was already started is stopped again.
func startWorker(cfg *config.Config, channels *channel.Registry) (w *worker, err error) {
	authCfg = cfg.Server

	if err := crypto.ValidateKeyConfigured(); err != nil {
		return nil, fmt.Errorf("encryption key validation failed: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w = &worker{cancel: cancel}
	defer func() {
		if err != nil {
			w.runCleanup()
			cancel()
			w = nil
		}
	}()

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
		return w, fmt.Errorf("redis store: %w", err)
	}
	w.onStop(func() { _ = store.Close() })

	// 5. Initialize RabbitMQ Consumer
	var consumer *broker.Consumer
	for i := 0; i < 5; i++ {
		// Parallel across users, ordered per user; RABBITMQ_PREFETCH bounds concurrent turns (P2-8).
		consumer, err = broker.NewConsumerWithOptions(cfg.RabbitMQ.URL, broker.InboundMessagesQueue, broker.WithPrefetch(cfg.RabbitMQ.Prefetch))
		if err == nil {
			break
		}
		slog.Warn("failed to initialize rabbitmq consumer, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return w, fmt.Errorf("rabbitmq consumer: %w", err)
	}
	w.onStop(func() { _ = consumer.Close() })

	// 4. Initialize Gemini LLM Client
	llmClient, err := llm.NewClient(ctx, cfg.Gemini.APIKey, cfg.Gemini.Model)
	if err != nil {
		return w, fmt.Errorf("gemini client: %w", err)
	}

	// 5. Initialize OAuth Config
	oauthCfg := calendar.NewOAuthConfig(&cfg.Google)

	// 6. Initialize Groq Audio Client
	audioClient := audio.NewClient(cfg.Groq.APIKey)

	// 7. Initialize Database Client
	dbURL := cfg.Database.URL
	if dbURL == "" {
		return w, errors.New("DATABASE_URL is required")
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
		return w, fmt.Errorf("database: %w", err)
	}
	w.onStop(func() {
		if err := dbClient.Close(); err != nil {
			slog.Error("failed to close db connection pool", "error", err)
		}
	})
	// P4-3: never run against a schema with pending migrations.
	sqlDB, err := dbClient.DB.DB()
	if err != nil {
		return w, fmt.Errorf("database handle: %w", err)
	}
	if err = db.EnsureMigrated(ctx, sqlDB); err != nil {
		return w, err
	}

	gmailClient := gmail.NewClient(oauthCfg, store)
	tasksClient := tasks.NewClient(oauthCfg, store)
	contactsClient := contacts.NewClient(oauthCfg, store)
	tavilyClient := tavily.NewClient(cfg.Tavily.APIKey)

	// 8.5 Initialize Telemetry Publisher
	telemetryPublisher, err := broker.NewSafePublisher(cfg.RabbitMQ.URL, broker.AuditEventsQueue)
	if err != nil {
		return w, fmt.Errorf("telemetry publisher: %w", err)
	}
	w.onStop(func() { _ = telemetryPublisher.Close() })

	// ALLOWED_USERS seeds the first households on a fresh install only (V4-30, ADR 0002
	// Decision 6); once a household exists the database is the single source of truth.
	allowed := cfg.ParseAllowedUsers()
	boot, err := dbClient.BootstrapHouseholds(context.Background(), allowed, cfg.Server.DefaultTimezone)
	if err != nil {
		return w, err
	}
	db.LogBootstrap(boot, len(allowed) > 0)

	// 8.6 Initialize Wave 1 EventBus with dual-lane worker pool and Domain ToolModules
	eventBus := events.NewEventBus(events.WithWorkerCount(4), events.WithQueueCapacity(1024))
	w.bus = eventBus
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

	// Proactive Notification Sender for Shared Spaces and Automations: these callers hold only the
	// recipient's ID, so the adapter that recognizes it sends (V4-31; V4-32 replaces the guess with
	// the person's linked channel accounts).
	notifier := channelNotifier{channels: channels}
	notificationSender := notifier.SendMessage

	// Track 1C: Shared Spaces & Partner Sync Notifier
	sharedNotifier := shared.NewNotifier(dbClient, notificationSender)
	sharedNotifier.RegisterWithBus(eventBus)
	sharedModule := shared.NewModule(dbClient, eventBus, sharedNotifier)

	// Track 1D: Multi-Schedule & Virtual Sub-Calendars
	calendarFactory := orchestrator.NewCalendarFactory(store.GetOAuthToken, oauthCfg)
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
	engine := orchestrator.NewEngine(consumer, store, dbClient, llmClient, channels, oauthCfg, gmailClient, tasksClient, contactsClient, tavilyClient, audioClient, telemetryPublisher, cfg.Server.DefaultTimezone).
		WithToolRegistry(registry).
		WithEventBus(eventBus).
		WithCalendarConventions(orchestrator.ParseCalendarConventions(cfg.Server.WeekStartDay, cfg.Server.WeekendDays)).
		WithHealthReporter(newHealthReporter(cfg, channels, store, dbClient, oauthCfg, doctorBackupDir)) // /doctor (V4-17)

	// Start Cron jobs
	c := scheduler.StartCronJobs(engine, dbClient, automationRunner, cfg.Server.DefaultTimezone)
	w.onStop(func() { c.Stop() })

	engine.SetWorkflowsChangedCallback(func() {
		scheduler.ReloadWorkflows(engine, dbClient, automationRunner, cfg.Server.DefaultTimezone)
	})

	// Run engine in a goroutine
	w.engine = engine
	go func() {
		slog.Info("worker engine starting...")
		if err := engine.Start(ctx); err != nil {
			slog.Error("engine stopped with error", "error", err)
		}
	}()

	// HTTP routes for the OAuth callback and GraphQL
	w.router = setupAppRouter(oauthCfg, store, notifier, dbClient, durabilityModule, eventBus)
	return w, nil
}

// onStop registers a cleanup; stop runs them in reverse order of registration.
func (w *worker) onStop(f func()) { w.cleanup = append(w.cleanup, f) }

func (w *worker) runCleanup() {
	for i := len(w.cleanup) - 1; i >= 0; i-- {
		w.cleanup[i]()
	}
	w.cleanup = nil
}

// stop ends the worker in the orchestrator's order: stop consuming, drain the event bus, wait for
// the engine's background tasks, then stop the scheduler and close the connections. Call it after
// the HTTP server has stopped accepting requests.
func (w *worker) stop() {
	w.cancel()
	busCtx, busCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer busCancel()
	if err := w.bus.Close(busCtx); err != nil {
		slog.Error("failed to gracefully drain event bus", "error", err)
	}
	w.engine.Wait()
	w.runCleanup()
}

// AuthNotifier tells a person in chat that their Google sign-in finished.
type AuthNotifier interface {
	SendMessage(ctx context.Context, userID string, text string) error
}

// channelNotifier sends to a person known only by their ID, on the channel whose adapter
// recognizes it.
type channelNotifier struct {
	channels *channel.Registry
}

func (n channelNotifier) SendMessage(ctx context.Context, recipientID, text string) error {
	to, ok := n.channels.Recognize(recipientID)
	if !ok {
		return fmt.Errorf("no channel recognizes the recipient %q", recipientID)
	}
	return n.channels.SendText(ctx, to, text)
}

type CredentialFallbackSaver interface {
	SaveCredentialFallback(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error
}

func setupAuthRouter(oauthCfg *calendar.OAuthConfig, store *state.Store, notifier AuthNotifier, fallbackSaver ...CredentialFallbackSaver) *http.ServeMux {
	var saver CredentialFallbackSaver
	if len(fallbackSaver) > 0 {
		saver = fallbackSaver[0]
	}
	return setupAppRouter(oauthCfg, store, notifier, nil, saver)
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

func setupAppRouter(oauthCfg *calendar.OAuthConfig, store *state.Store, notifier AuthNotifier, dbClient *db.Client, fallbackSaver CredentialFallbackSaver, eventBus ...*events.EventBus) *http.ServeMux {
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

	mux.HandleFunc(config.GoogleCallbackPath, func(w http.ResponseWriter, r *http.Request) {
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

		// Tell the person in chat, on their channel
		if notifier != nil {
			_ = notifier.SendMessage(r.Context(), targetUserID, "✅ Successfully connected to Google Calendar! You can now ask me to schedule events.")
		}

		// Tell the browser they can close the window
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Success!</h1><p>Google Calendar connected. You can close this window and return to the chat.</p>"))
	})
	return mux
}
