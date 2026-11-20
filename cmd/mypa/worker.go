package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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
	"github.com/nivik/mypa/internal/domains/preferences"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/gmail"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/nivik/mypa/internal/quiet"
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
	var lanes personLanes // filled once the database is open, below
	for i := 0; i < 5; i++ {
		// Parallel across people, ordered per person whichever account they write from (V4-32);
		// RABBITMQ_PREFETCH bounds concurrent turns (P2-8).
		consumer, err = broker.NewConsumerWithOptions(cfg.RabbitMQ.URL, broker.InboundMessagesQueue,
			broker.WithPrefetch(cfg.RabbitMQ.Prefetch), broker.WithLanes(lanes.of))
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
	lanes.set(dbClient)
	// Accounts Migration B made have no channel yet: no code outside the adapters names a platform,
	// so the adapter that recognizes each ID names it, before any message is taken (V4-32).
	resolved, unknown, err := dbClient.ResolveAccountChannels(ctx, func(id string) string {
		if to, ok := channels.Recognize(id); ok {
			return to.Channel
		}
		return ""
	})
	if err != nil {
		return w, fmt.Errorf("resolve the channels of channel accounts: %w", err)
	}
	if resolved > 0 {
		slog.Info("filled the channel of channel accounts made by the upgrade", "accounts", resolved)
	}
	if unknown > 0 {
		slog.Warn("channel accounts whose ID no adapter recognizes cannot write until fixed", "accounts", unknown)
	}
	// A person's Google sign-in is found through all their accounts, preferred first (V4-32).
	store.SetAccountKeys(dbClient.AccountKeysOf)

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
	boot, err := dbClient.BootstrapHouseholds(context.Background(), allowed, cfg.Server.DefaultTimezone,
		bootstrapChannelOf(cfg.AllowedUserChannels(), channels))
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

	// V4-33: every message the assistant starts itself goes through the delivery policy, which
	// records it, chooses a route from each channel's rule and holds what no channel takes yet.
	// A reminder that goes out late joins the person's chat history through the engine, which is
	// built below; nothing is delivered before the engine starts.
	var recordLate func(ctx context.Context, d models.ProactiveDelivery)
	delivery := proactive.New(dbClient, dbClient, channels,
		proactive.WithZone(personZone(dbClient, cfg.Server.DefaultTimezone)),
		proactive.WithLateSend(func(ctx context.Context, d models.ProactiveDelivery) {
			if recordLate != nil {
				recordLate(ctx, d)
			}
		}))

	// Proactive Notification Sender for Shared Spaces and Automations: these callers hold only the
	// recipient's person key, so the message goes to the account that person wants notifications
	// on (V4-32), through the delivery policy (V4-33).
	notifier := channelNotifier{channels: channels, accounts: dbClient, delivery: delivery}
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
	automationRunner := automation.NewRunner(dbClient, eventBus, automation.WithNotificationSender(notificationSender),
		automation.WithHouseholdPacks(dbClient.HouseholdPacks)) // rules run only with the Automations pack on (V4-27)
	automationModule := automation.NewModule(dbClient, automation.WithEventBus(eventBus), automation.WithRunner(automationRunner))

	// Core: a person's preferences and the personal CRM, outside every pack (V4-27)
	preferencesModule := preferences.NewModule(dbClient, preferences.WithEventBus(eventBus))

	// Register all Wave 0, Wave 1, and Wave 2 domain modules in the ToolRegistry
	workspaceGmailModule := workspace_gmail.NewModule(workspace_gmail.WithClient(gmailClient), workspace_gmail.WithEventBus(eventBus))
	workspaceTasksModule := workspace_tasks.NewModule(workspace_tasks.WithClient(tasksClient), workspace_tasks.WithEventBus(eventBus))
	workspaceContactsModule := workspace_contacts.NewModule(workspace_contacts.WithClient(contactsClient), workspace_contacts.WithEventBus(eventBus))
	webResearchModule := web_research.NewModule(web_research.WithClient(tavilyClient))

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
	registry.Register(preferencesModule)

	// Wire registry declarations into llmClient
	llmClient.AddFunctionDeclarations(registry.AllDeclarations()...)

	// 9. Initialize Engine with ToolRegistry and EventBus
	engine := orchestrator.NewEngine(consumer, store, dbClient, llmClient, channels, oauthCfg, gmailClient, tasksClient, contactsClient, tavilyClient, audioClient, telemetryPublisher, cfg.Server.DefaultTimezone).
		WithToolRegistry(registry).
		WithEventBus(eventBus).
		WithCalendarConventions(orchestrator.ParseCalendarConventions(cfg.Server.WeekStartDay, cfg.Server.WeekendDays)).
		WithHealthReporter(newHealthReporter(cfg, channels, store, dbClient, oauthCfg, doctorBackupDir)). // /doctor (V4-17)
		WithAccounts(dbClient, []byte(strings.TrimSpace(cfg.Server.LinkCodeSecret))).                     // /link (V4-32)
		WithSignIn(dbClient, cfg.Server.DashboardURL).                                                    // /dashboard (V4-35)
		WithQuietHours(dbClient).                                                                         // each person's zone and quiet hours (V4-05)
		WithDelivery(delivery)                                                                            // reminders, briefings and notifications (V4-33)
	recordLate = engine.RecordLateDelivery
	if strings.TrimSpace(cfg.Server.LinkCodeSecret) == "" {
		slog.Warn("LINK_CODE_SECRET is not set: linking a second app to a person (/link) is off")
	}
	if n, err := dbClient.HouseholdsWithoutOwner(ctx); err == nil && n > 0 {
		slog.Warn("households without an owner: nobody can manage them or their members; assign one with `mypa access grant owner <person>`", "households", n)
	}
	stopPurge := make(chan struct{})
	go purgeSignInsEvery(dbClient.PurgeSignIns, stopPurge)
	w.onStop(func() { close(stopPurge) })

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

// deliveryAccounts finds the account a person wants messages the assistant starts on.
type deliveryAccounts interface {
	DeliveryAccount(ctx context.Context, key string) (*models.ChannelAccount, error)
}

// channelNotifier sends to a person known only by their person key (partner notifications,
// automations, the Google sign-in confirmation): through the delivery policy when it is set
// (V4-33), else to the account they want notifications on (V4-32). A key that names no person
// falls back to the channel whose adapter recognizes it.
type channelNotifier struct {
	channels *channel.Registry
	accounts deliveryAccounts
	delivery notificationPolicy
}

// notificationPolicy is the part of the delivery policy the notifier uses.
type notificationPolicy interface {
	Deliver(ctx context.Context, req proactive.Request) (*models.ProactiveDelivery, error)
}

// personZone is the time zone of a person, by their ID, for the note on a late message: their
// household's, else the install's.
func personZone(c *db.Client, fallback string) func(ctx context.Context, personID string) *time.Location {
	return func(ctx context.Context, personID string) *time.Location {
		var s quiet.Settings
		if u, err := c.UserByID(ctx, personID); err == nil && u != nil {
			s, _ = c.QuietSettings(ctx, u.PlatformID)
		}
		return quiet.NewClock(s, fallback).Loc
	}
}

// JoinSteps is what a new person does on each configured channel before their first message
// (V4-41), for the dashboard's pairing codes.
func (n channelNotifier) JoinSteps() []channel.JoinStep { return n.channels.JoinSteps() }

// joinStepsOf returns the notifier's JoinSteps when it is the channel layer's, else nil.
func joinStepsOf(notifier AuthNotifier) func() []channel.JoinStep {
	if js, ok := notifier.(interface{ JoinSteps() []channel.JoinStep }); ok {
		return js.JoinSteps
	}
	return nil
}

func (n channelNotifier) SendMessage(ctx context.Context, recipientID, text string) error {
	if n.delivery != nil {
		_, err := n.delivery.Deliver(ctx, proactive.Request{Key: recipientID, Category: proactive.Notification, Text: text})
		if !errors.Is(err, proactive.ErrNoPerson) {
			return err
		}
	}
	if n.accounts != nil {
		a, err := n.accounts.DeliveryAccount(ctx, recipientID)
		if err != nil {
			return fmt.Errorf("find the recipient's account: %w", err)
		}
		if a != nil {
			return n.channels.SendText(ctx, channel.Address{Channel: a.Channel, Person: a.ExternalID}, text)
		}
	}
	to, ok := n.channels.Recognize(recipientID)
	if !ok {
		return fmt.Errorf("no channel recognizes the recipient %q", recipientID)
	}
	return n.channels.SendText(ctx, to, text)
}

// personLanes names the processing lane of an inbound message: its person (V4-32). Until the
// database is open, and for an account nobody owns, it answers "" (one lane per sender).
type personLanes struct {
	mu sync.RWMutex
	db *db.Client
}

func (l *personLanes) set(c *db.Client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.db = c
}

func (l *personLanes) of(msg channel.Inbound) string {
	l.mu.RLock()
	c := l.db
	l.mu.RUnlock()
	if c == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	person, err := c.PersonOfAccount(ctx, msg.Channel, msg.Sender)
	if err != nil {
		slog.Warn("could not find the sender's person for ordering; using the sender's own lane", "error", err)
		return ""
	}
	return person
}

// bootstrapChannelOf names the channel of an ALLOWED_USERS entry: its fifth field when that names
// a registered channel, else the channel whose adapter recognizes the ID (V4-32).
func bootstrapChannelOf(fifth map[string]string, channels *channel.Registry) func(string) string {
	return func(platformID string) string {
		if ch, ok := fifth[platformID]; ok {
			if _, known := channels.Get(ch); known {
				return ch
			}
			slog.Warn("an ALLOWED_USERS entry names a channel that is not registered", "channel", ch)
			return ""
		}
		if to, ok := channels.Recognize(platformID); ok {
			return to.Channel
		}
		return ""
	}
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
		var promptCache graph.PromptInvalidator // no typed nil: a nil store means no cache
		if store != nil {
			promptCache = store
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
			PromptCache:    promptCache,
			JoinStepsOf:    joinStepsOf(notifier), // V4-41: shown with pairing codes
		})
		srv := graph.NewServer(resolvers) // with the pack guard (V4-27)

		// V4-35: the only credential is a dashboard session (a person signed in with /dashboard);
		// the person's role and household are read from the database on every request.
		authMiddleware := graph.NewAuthMiddleware(dbClient, dbClient)
		mux.Handle(SignInPath, newSignInHandler(dbClient, dbClient, notifier))

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
