package orchestrator

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/logging"
	"google.golang.org/genai"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer           *broker.Consumer
	store              *state.Store
	backups            backupOps // pg_dump and backup-directory seam; zero value = real db functions
	db                 DBClient
	llm                LLMClient
	channels           *channel.Registry // every channel adapter (V4-31); the engine names none
	audioClient        AudioClient
	oauthCfg           *calendar.OAuthConfig
	gmailClient        GmailClient
	tasksClient        TasksClient
	contactsClient     ContactsClient
	tavilyClient       TavilyClient
	calendarFactory    func(context.Context, string) (CalendarClient, error)
	timezone           string
	publisher          EventPublisher
	wg                 sync.WaitGroup
	onWorkflowsChanged func()
	toolRegistry       *Registry
	eventBus           *events.EventBus
	intentMatcher      IntentMatcher
	calendar           CalendarConventions // week start / weekend for the system prompt (P2-6)
	health             HealthReporter      // the /doctor report (V4-17); nil when not wired
	accounts           AccountStore        // people's channel accounts and link codes (V4-32); nil when not wired
	codeKey            []byte              // the server secret typed codes are hashed under (LINK_CODE_SECRET)
	signIns            SignInStore         // dashboard sign-in links and sessions (V4-35); nil when not wired
	dashboardURL       string              // where a sign-in link points (DASHBOARD_URL)
}

var hebrewPrepositionRegex = regexp.MustCompile(`(^|[^\p{Hebrew}\w])([בלמכ])(\d+)`)

// NormalizeHebrewPrepositionNumbers inserts a hyphen between Hebrew preposition letters and attached numbers (e.g., "ב18" -> "ב-18").
// This prevents automated false-positive safety/content filter blocks in LLM tokenizers.
func NormalizeHebrewPrepositionNumbers(text string) string {
	return hebrewPrepositionRegex.ReplaceAllString(text, "${1}${2}-${3}")
}

// SetWorkflowsChangedCallback sets a callback to be invoked when workflows change.
func (e *Engine) SetWorkflowsChangedCallback(cb func()) {
	e.onWorkflowsChanged = cb
}

// SetToolRegistry sets the modular tool registry and synchronizes declarations with the LLM client.
func (e *Engine) SetToolRegistry(reg *Registry) {
	e.toolRegistry = reg
	if reg != nil && e.llm != nil {
		if toolSetter, ok := e.llm.(interface {
			SetExtraTools([]*genai.FunctionDeclaration)
		}); ok {
			toolSetter.SetExtraTools(reg.AllDeclarations())
		}
	}
	if reg != nil && e.store != nil && e.eventBus != nil {
		RegisterPromptInvalidator(e.eventBus, e.store)
		reg.WithPromptCache(e.store)
	}
}

// WithToolRegistry configures the tool registry in a builder pattern.
func (e *Engine) WithToolRegistry(reg *Registry) *Engine {
	e.SetToolRegistry(reg)
	return e
}

// SetEventBus sets the internal event bus.
func (e *Engine) SetEventBus(bus *events.EventBus) {
	e.eventBus = bus
	if e.toolRegistry != nil && e.store != nil && bus != nil {
		RegisterPromptInvalidator(bus, e.store)
		e.toolRegistry.WithPromptCache(e.store)
	}
}

// WithEventBus configures the event bus in a builder pattern.
func (e *Engine) WithEventBus(bus *events.EventBus) *Engine {
	e.SetEventBus(bus)
	return e
}

// WithIntentMatcher sets a custom IntentMatcher on the engine.
func (e *Engine) WithIntentMatcher(im IntentMatcher) *Engine {
	e.intentMatcher = im
	return e
}

// WithCalendarConventions sets the household's week start / weekend (WEEK_START_DAY, WEEKEND_DAYS).
func (e *Engine) WithCalendarConventions(c CalendarConventions) *Engine {
	e.calendar = c
	return e
}

// location returns the household's time zone, else the install's DEFAULT_TIMEZONE, else UTC
// (an unset or invalid zone falls through to the next).
func (e *Engine) location(s identity.Scope) *time.Location {
	for _, tz := range []string{s.TimeZone(""), e.timezone} {
		if tz = strings.TrimSpace(tz); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	return time.UTC
}

// formatAttachmentMarker formats a concise human-readable metadata marker for stripped attachments.
func formatAttachmentMarker(mimeType string, dataLen int) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	sizeKB := float64(dataLen) / 1024.0
	if sizeKB >= 1024.0 {
		return fmt.Sprintf("[Attachment: %s (%.1f MB)]", mimeType, sizeKB/1024.0)
	}
	return fmt.Sprintf("[Attachment: %s (%.1f KB)]", mimeType, sizeKB)
}

// buildCallContext constructs a CallContext for the given user and incoming message.
func (e *Engine) buildCallContext(ctx context.Context, user *models.User, msg channel.Inbound) CallContext {
	sendNotif := func(nCtx context.Context, targetUserID, text string) error {
		if targetUserID == msg.Sender || (user != nil && targetUserID == user.PlatformID) {
			return e.sendMessage(nCtx, msg, text)
		}
		// Someone else: where they want notifications (V4-32).
		if err := e.deliverTo(nCtx, targetUserID, text); err != nil {
			return fmt.Errorf("no active channel found for user: %s", targetUserID)
		}
		return nil
	}

	callCtx := CallContext{
		User:             user,
		Scope:            e.scopeOf(ctx, user),
		Message:          msg,
		DB:               e.db,
		LLM:              e.llm,
		EventBus:         e.eventBus,
		SendNotification: sendNotif,
		Extra:            make(map[string]interface{}),
	}
	if e.onWorkflowsChanged != nil {
		callCtx.Extra["workflows_changed"] = e.onWorkflowsChanged
	}
	if secState := GetTurnSecurityState(ctx); secState != nil {
		isUntrusted, sources := secState.IsUntrusted()
		callCtx.HasUntrustedContent = isUntrusted
		callCtx.UntrustedSources = sources
	}
	return callCtx
}

// NewEngine initializes the orchestrator engine.
func NewEngine(
	cons *broker.Consumer,
	store *state.Store,
	db DBClient,
	llm LLMClient,
	channels *channel.Registry,
	oauth *calendar.OAuthConfig,
	gmailC GmailClient,
	tasksC TasksClient,
	contactsC ContactsClient,
	tavilyC TavilyClient,
	audio AudioClient,
	pub EventPublisher,
	tz string,
) *Engine {
	e := &Engine{
		consumer:       cons,
		store:          store,
		db:             db,
		llm:            llm,
		channels:       channels,
		oauthCfg:       oauth,
		gmailClient:    gmailC,
		tasksClient:    tasksC,
		contactsClient: contactsC,
		tavilyClient:   tavilyC,
		audioClient:    audio,
		publisher:      pub,
		timezone:       tz,
		intentMatcher:  NewKeywordIntentMatcher(),
		calendar:       DefaultCalendarConventions(),
	}

	e.calendarFactory = NewCalendarFactory(e.store.GetOAuthToken, e.oauthCfg)

	return e
}

// Start begins consuming messages and processing them.
func (e *Engine) Start(ctx context.Context) error {
	slog.Info("orchestrator engine starting")

	return e.consumer.ConsumeWithInfo(func(msg channel.Inbound, info broker.DeliveryInfo) error {
		// Use a bounded timeout for each message processing to prevent stuck consumers, but allow enough time for multi-turn agent execution (5m)
		msgCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		if err := e.ProcessMessage(msgCtx, msg); err != nil {
			slog.Error("failed to process message", "message_id", msg.MessageID, "attempt", info.Attempt, "max_attempts", info.MaxAttempts, "error", err)

			// Notify the user only when no retry will follow (final attempt or non-retryable).
			if shouldNotifyUserOfFailure(err, info) {
				notifyCtx, notifyCancel := context.WithTimeout(context.WithoutCancel(msgCtx), 15*time.Second)
				_ = e.sendMessage(notifyCtx, msg, "⚠️ Sorry, I encountered an internal error while processing your request.")
				notifyCancel()
			}

			// Retryable errors are requeued (DLQ after max attempts); ErrNonRetryable is acked.
			return err
		}

		return nil
	})
}

// Wait blocks until all background tasks finish.
func (e *Engine) Wait() {
	e.wg.Wait()
}

// sendMessage sends assistant text back to where msg came from. The channel's adapter converts
// the text to its markup and splits it at its length limit (V4-31).
func (e *Engine) sendMessage(ctx context.Context, msg channel.Inbound, text string) error {
	return e.sendTo(ctx, msg.ReplyAddress(), text)
}

// sendTo sends assistant text to an address on any channel.
func (e *Engine) sendTo(ctx context.Context, to channel.Address, text string) error {
	if e.channels == nil {
		return fmt.Errorf("no channels configured: cannot send to %q", to.Channel)
	}
	return e.channels.SendText(ctx, to, text)
}

// lastAddress is where the assistant reaches a person it is not answering: the channel and chat of
// their latest audit session. Proactive delivery gets its own rule in V4-33.
func lastAddress(log *models.AuditSession, userID string) channel.Address {
	return channel.Address{Channel: log.Source, Conversation: log.ChatID, Person: userID}
}

func (e *Engine) handleUnauthenticatedUser(ctx context.Context, msg channel.Inbound) error {
	// Check if user is currently locked out
	if e.store != nil {
		if locked, remaining, lockErr := e.store.CheckPairingLockout(ctx, msg.Sender); lockErr == nil && locked {
			mins := int(remaining.Minutes()) + 1
			return e.sendMessage(ctx, msg, fmt.Sprintf("🚫 Too many failed pairing attempts. You are temporarily locked out. Please try again in %d minutes.", mins))
		}
	}

	// A link code from an account that belongs to nobody yet (V4-32).
	if linkCode, ok := linkCodeArg(msg.Text); ok {
		_, err := e.redeemLinkCode(ctx, nil, msg, linkCode)
		return err
	}

	// Extract potential pairing code from message text
	code := strings.TrimSpace(msg.Text)
	code = strings.TrimPrefix(code, "/start ")
	code = strings.TrimSpace(code)

	var pc *models.PairingCode
	if len(code) == 6 {
		pc, _ = e.db.GetPairingCode(code)
	}

	if pc != nil {
		newUser := models.User{
			PlatformID:  msg.Sender,
			Name:        "Unknown",
			Role:        pc.Role,
			HouseholdID: pc.HouseholdID, // the code's household (V4-30); the label is still written
			FamilyGroup: pc.FamilyGroup,
			CreatedAt:   time.Now(),
		}
		if err := e.db.RedeemPairingCode(pc.ID, newUser, msg.Channel); err == nil {
			markSideEffect(ctx) // code consumed: a retry would see it as invalid and count a failed attempt
			if e.store != nil {
				_ = e.store.ResetPairingRateLimit(ctx, msg.Sender)
			}
			return e.sendMessage(ctx, msg, "✅ Pairing successful! Welcome to MyPA. You can now use all features.")
		}
	}

	// Record failed pairing attempt
	if e.store != nil {
		attempts, lockedOut, _ := e.store.RecordFailedPairingAttempt(ctx, msg.Sender)
		markSideEffect(ctx) // attempt counted: a retry would count it twice toward lockout
		if lockedOut {
			return e.sendMessage(ctx, msg, "🚫 Too many failed attempts (5/5). Your account has been locked for 1 hour.")
		}
		remainingAttempts := 5 - attempts
		return e.sendMessage(ctx, msg, fmt.Sprintf("🚫 Unauthorized access. Please provide a valid 6-digit pairing code to continue. (%d attempts remaining before 1-hour lockout)", remainingAttempts))
	}

	return e.sendMessage(ctx, msg, "🚫 Unauthorized access. Please provide a valid 6-digit pairing code to continue.")
}

// ProcessMessage is the main entry point for handling an incoming user message.
// It orchestrates transcription, command handling, confirmation flows, and LLM reasoning.
func (e *Engine) ProcessMessage(ctx context.Context, msg channel.Inbound) (err error) {
	// P1-4: drop duplicate deliveries (webhook retries, broker redeliveries) before
	// doing anything — including telemetry — so a message's tools run at most once.
	claim, duplicate := e.claimInbound(ctx, msg)
	if duplicate {
		slog.Info("duplicate inbound message skipped", "message_id", msg.MessageID, "user_id", msg.Sender, "source", msg.Channel)
		return nil
	}

	logging.LogMessageText(ctx, nil, "processing message", withholdCodes(msg.Text), "user_id", msg.Sender, "message_id", msg.MessageID) // P3-3

	var llmResponse string
	// The audit session is recorded under the person's key once the sender is known (V4-32), so a
	// person's sessions from every account are one person's; the account is in Source and ChatID.
	auditUser := msg.Sender
	sessionID := uuid.New().String()
	startTime := time.Now()
	// Per-turn record of side effects and executed tools (P1-4, P2-2, P2-9). Declared before the
	// telemetry defer so the audit session can carry the turn's tool summary.
	effects := &turnEffects{}

	defer func() {
		e.wg.Add(1)
		// Publish the telemetry session asynchronously
		go func(msgText string, errStr string, toolSummary string) {
			defer e.wg.Done()
			status := "success"
			if errStr != "" {
				status = "failed"
			}

			session := models.AuditSession{
				ID:              sessionID,
				UserID:          auditUser,
				ChatID:          msg.Conversation.ID,
				Source:          msg.Channel,
				UserPrompt:      msgText,
				FinalResponse:   llmResponse,
				StartTime:       startTime,
				EndTime:         time.Now(),
				TotalDurationMs: time.Since(startTime).Milliseconds(),
				Status:          status,
				ToolSummary:     toolSummary,
			}

			if e.publisher != nil {
				pubErr := e.publisher.Publish(context.Background(), session)
				if pubErr != nil {
					slog.Error("failed to publish telemetry session", "error", pubErr)
				}
			}
		}(withholdCodes(msg.Text), func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(), effects.toolSummary())
	}()

	secState := &TurnSecurityState{}
	ctx = WithTurnSecurityState(ctx, secState)

	// Runs before the telemetry defer above (LIFO), so telemetry sees the final error.
	ctx = withTurnEffects(ctx, effects)
	defer func() {
		if err != nil && effects.any() {
			err = broker.NonRetryable(err)
		}
		claim.finish(ctx, err)
	}()

	// 1. Pre-Auth Check: the channel account the message came from must belong to a person
	// (V4-32); every account of one person resolves to the same users row.
	user, err := e.db.UserForAccount(ctx, msg.Channel, msg.Sender)
	if err != nil || user == nil {
		return e.handleUnauthenticatedUser(ctx, msg)
	}
	auditUser = user.PlatformID
	if e.accounts != nil && !isStartedByAssistant(ctx) {
		if rerr := e.accounts.RecordInbound(ctx, msg.Channel, msg.Sender); rerr != nil {
			slog.Warn("could not record the account's last message time", "error", rerr)
		}
	}
	ctx = withTurnUser(ctx, user) // P2-7: resolved once; tool calls reuse it
	// V4-30: the household is read from the database once per message, from the sender's row.
	scope, scopeErr := identity.ForUser(ctx, e.db, user)
	if scopeErr != nil {
		slog.Warn("sender has no usable household", "user_id", user.PlatformID, "error", scopeErr)
		return e.sendMessage(ctx, msg, "🚫 Your account is not attached to a household. Ask an admin to add you again.")
	}
	ctx = withTurnScope(ctx, scope)

	// 2. Account linking's own confirmation (V4-32), then staged-action confirmation
	// ("CONFIRM [TOOL]") and slash commands (commands.go)
	if identity.IsConfirmLink(msg.Text) {
		response, cerr := e.confirmLink(ctx, user, msg)
		llmResponse = response
		return cerr
	}
	if identity.IsConfirmMerge(msg.Text) {
		response, cerr := e.confirmMerge(ctx, user, msg)
		llmResponse = response
		return cerr
	}
	if response, handled, cerr := e.handleConfirmation(ctx, user, msg); handled {
		llmResponse = response
		return cerr
	}
	if response, handled, cerr := e.dispatchCommand(ctx, user, msg); handled {
		llmResponse = response
		return cerr
	}

	// 3. Attachments: download / transcribe / inline (media.go)
	media, stop, err := e.ingestMedia(ctx, &msg, secState)
	if stop {
		return err
	}

	// 4. The LLM turn (turn.go)
	response, err := e.runTurn(ctx, user, msg, media, effects)
	llmResponse = response
	return err
}

// BroadcastProactiveMessage sends a scheduled prompt to all known users.
func (e *Engine) BroadcastProactiveMessage(ctx context.Context, promptText string) {
	slog.Info("broadcasting proactive message to all users")

	userIDs, err := e.db.GetUniqueUsers()
	if err != nil {
		slog.Error("failed to get unique users for broadcast", "error", err)
		return
	}

	for _, userID := range userIDs {
		// A system-triggered prompt fed into a turn on behalf of the person, on the account they
		// want notifications on (V4-32).
		to, _, err := e.deliveryAddress(ctx, userID)
		if err != nil {
			slog.Warn("no channel to send the briefing on", "user_id", userID, "error", err)
			continue
		}

		msg := channel.Inbound{
			Channel:      to.Channel,
			MessageID:    fmt.Sprintf("cron-%d", time.Now().Unix()),
			Sender:       to.Person,
			Conversation: channel.Conversation{ID: to.Conversation},
			Text:         promptText,
		}

		// Run in a separate goroutine so one user doesn't block another
		go func(m channel.Inbound) {
			// Create a background context specifically for this broadcast
			bgCtx, cancel := context.WithTimeout(startedByAssistant(context.Background()), 2*time.Minute)
			defer cancel()

			slog.Info("sending proactive message", "user_id", m.Sender)
			if err := e.ProcessMessage(bgCtx, m); err != nil {
				slog.Error("failed to process proactive message", "user", m.Sender, "error", err)
			}
		}(msg)
	}
}

// CheckAndSendReminders checks the database for due reminders and sends them.
func (e *Engine) CheckAndSendReminders() {
	reminders, err := e.db.GetDueReminders()
	if err != nil {
		slog.Error("failed to get due reminders", "error", err)
		return
	}

	for _, reminder := range reminders {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		to, historyKey, err := e.deliveryAddress(ctx, reminder.UserID)
		if err != nil {
			slog.Warn("no channel to send the reminder on", "user_id", reminder.UserID, "error", err)
			cancel()
			continue
		}

		formattedMsg := fmt.Sprintf("🔔 *Reminder:*\n\n%s", reminder.Message)

		err = e.sendTo(ctx, to, formattedMsg)
		if err != nil {
			slog.Error("failed to send reminder message", "reminder_id", reminder.ID, "error", err)
			cancel()
			continue
		}
		cancel()

		_ = e.db.MarkReminderSent(reminder.ID)

		// Optionally log to history
		if e.store != nil {
			_ = e.store.AppendChatHistory(context.Background(), historyKey, models.ChatMessage{Role: "assistant", Content: formattedMsg})
		}
	}

	sharedReminders, err := e.db.GetDueSharedReminders()
	if err != nil {
		slog.Error("failed to get due shared reminders", "error", err)
		return
	}

	for _, s := range sharedReminders {
		targetUserID := s.AssignedToUserID
		if targetUserID == "" {
			targetUserID = s.CreatedByUserID
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		to, historyKey, err := e.deliveryAddress(ctx, targetUserID)
		if err != nil {
			slog.Warn("no channel to send the shared reminder on", "user_id", targetUserID, "shared_reminder_id", s.ID)
			cancel()
			continue
		}
		formattedMsg := fmt.Sprintf("🔔 *Shared Task Reminder:*\n\n**%s** (Topic: %s)", s.Title, s.Topic)
		err = e.sendTo(ctx, to, formattedMsg)
		if err != nil {
			slog.Error("failed to send shared reminder message", "shared_reminder_id", s.ID, "error", err)
			cancel()
			continue
		}
		cancel()

		// A scheduled job: the scope comes from the stored row's household (ADR 0002 Decision 3).
		if scope, serr := identity.ForStoredRow(context.Background(), e.db, s.HouseholdID, s.CreatedByUserID); serr == nil {
			_ = e.db.MarkSharedReminderSent(scope, s.ID)
		} else {
			slog.Error("shared reminder has no usable household", "shared_reminder_id", s.ID, "error", serr)
		}
		if e.store != nil {
			_ = e.store.AppendChatHistory(context.Background(), historyKey, models.ChatMessage{Role: "assistant", Content: formattedMsg})
		}
	}
}

func (e *Engine) handleUndo(ctx context.Context, user *models.User, msg channel.Inbound, mutationID string) error {
	scope := e.scopeOf(ctx, user)
	if e.toolRegistry != nil {
		if mod, ok := e.toolRegistry.GetModule("durability"); ok {
			if rEngine, ok := mod.(RollbackEngine); ok {
				res, err := rEngine.Rollback(ctx, scope, mutationID)
				if err != nil {
					if strings.Contains(err.Error(), "no recent undoable actions found") {
						return e.sendMessage(ctx, msg, "ℹ️ No recent undoable actions found.")
					}
					return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Undo failed: %v", err))
				}
				if res.AlreadyUndone {
					return e.sendMessage(ctx, msg, fmt.Sprintf("ℹ️ %s", res.Detail))
				}
				return e.sendMessage(ctx, msg, fmt.Sprintf("↩️ **Successfully Undone!**\n\n%s", res.Detail))
			}
		}
	}

	return e.sendMessage(ctx, msg, "⚠️ Durability undo engine is not configured.")
}
