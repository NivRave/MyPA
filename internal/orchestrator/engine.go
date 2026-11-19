package orchestrator

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/logging"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/markdown"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"

	"google.golang.org/genai"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer           *broker.Consumer
	store              *state.Store
	backups            backupOps // pg_dump/pg_restore seam; zero value = real db functions
	db                 DBClient
	llm                LLMClient
	tgClient           TelegramClient
	twClient           TwilioClient
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

// location returns the configured default timezone (UTC if unset or invalid).
func (e *Engine) location() *time.Location {
	if tz := strings.TrimSpace(e.timezone); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
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
func (e *Engine) buildCallContext(ctx context.Context, user *models.User, msg models.Message) CallContext {
	sendNotif := func(nCtx context.Context, targetUserID, text string) error {
		if targetUserID == msg.UserID {
			return e.sendMessage(nCtx, msg, text)
		}
		if e.db != nil {
			log, err := e.db.GetLastAuditSessionForUser(targetUserID)
			if err == nil && log != nil && log.Source != "" {
				targetMsg := models.Message{
					ID:     fmt.Sprintf("notif-%d", time.Now().UnixNano()),
					ChatID: log.ChatID,
					UserID: targetUserID,
					Text:   text,
					Source: log.Source,
				}
				return e.sendMessage(nCtx, targetMsg, text)
			}
		}
		return fmt.Errorf("no active channel found for user: %s", targetUserID)
	}

	callCtx := CallContext{
		User:             user,
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
	tg TelegramClient,
	tw TwilioClient,
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
		tgClient:       tg,
		twClient:       tw,
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

	return e.consumer.ConsumeWithInfo(func(msg models.Message, info broker.DeliveryInfo) error {
		// Use a bounded timeout for each message processing to prevent stuck consumers, but allow enough time for multi-turn agent execution (5m)
		msgCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		if err := e.ProcessMessage(msgCtx, msg); err != nil {
			slog.Error("failed to process message", "message_id", msg.ID, "attempt", info.Attempt, "max_attempts", info.MaxAttempts, "error", err)

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

// sendMessage sends a text message to the user, breaking it into smaller chunks if necessary to avoid platform limits.
func (e *Engine) sendMessage(ctx context.Context, msg models.Message, text string) error {
	if msg.Source == "whatsapp" {
		return e.twClient.SendMessage(ctx, msg.UserID, text)
	}

	formatted := text
	if msg.Source == "telegram" && !markdown.IsTelegramHTML(text) {
		formatted = markdown.ToTelegramHTML(text)
	}
	chunks := markdown.SafeChunkTelegramHTML(formatted, 4000)
	for _, chunk := range chunks {
		if err := e.tgClient.SendMessage(ctx, msg.ChatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) handleUnauthenticatedUser(ctx context.Context, msg models.Message) error {
	// Check if user is currently locked out
	if e.store != nil {
		if locked, remaining, lockErr := e.store.CheckPairingLockout(ctx, msg.UserID); lockErr == nil && locked {
			mins := int(remaining.Minutes()) + 1
			return e.sendMessage(ctx, msg, fmt.Sprintf("🚫 Too many failed pairing attempts. You are temporarily locked out. Please try again in %d minutes.", mins))
		}
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
			PlatformID:  msg.UserID,
			Name:        "Unknown",
			Role:        pc.Role,
			FamilyGroup: pc.FamilyGroup,
			CreatedAt:   time.Now(),
		}
		if err := e.db.RedeemPairingCode(pc.ID, newUser); err == nil {
			markSideEffect(ctx) // code consumed: a retry would see it as invalid and count a failed attempt
			if e.store != nil {
				_ = e.store.ResetPairingRateLimit(ctx, msg.UserID)
			}
			return e.sendMessage(ctx, msg, "✅ Pairing successful! Welcome to MyPA. You can now use all features.")
		}
	}

	// Record failed pairing attempt
	if e.store != nil {
		attempts, lockedOut, _ := e.store.RecordFailedPairingAttempt(ctx, msg.UserID)
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
func (e *Engine) ProcessMessage(ctx context.Context, msg models.Message) (err error) {
	// P1-4: drop duplicate deliveries (Telegram webhook retries, broker redeliveries) before
	// doing anything — including telemetry — so a message's tools run at most once.
	claim, duplicate := e.claimInbound(ctx, msg)
	if duplicate {
		slog.Info("duplicate inbound message skipped", "message_id", msg.ID, "user_id", msg.UserID, "source", msg.Source)
		return nil
	}

	logging.LogMessageText(ctx, nil, "processing message", msg.Text, "user_id", msg.UserID, "message_id", msg.ID) // P3-3

	var llmResponse string
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
				UserID:          msg.UserID,
				ChatID:          msg.ChatID,
				Source:          msg.Source,
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
		}(msg.Text, func() string {
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

	// 1. Pre-Auth Check: Verify user exists and is authenticated
	user, err := e.db.GetUser(msg.UserID)
	if err != nil || user == nil {
		return e.handleUnauthenticatedUser(ctx, msg)
	}
	ctx = withTurnUser(ctx, user) // P2-7: resolved once; tool calls reuse it

	// 2. Staged-action confirmation ("CONFIRM [TOOL]") and slash commands (commands.go)
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
		// Mock a message object to pass into processMessage
		// We'll treat this as a system-triggered prompt that we feed into the LLM on behalf of the user

		// To properly send the message, we need a valid chat ID and source.
		// We'll just look up the most recent audit session to find the user's preferred platform/chat ID.
		log, err := e.db.GetLastAuditSessionForUser(userID)
		if err != nil {
			slog.Warn("failed to get last audit session for user", "user_id", userID, "error", err)
			continue
		}

		if log.Source == "" {
			continue // Should not happen, but safeguard
		}

		msg := models.Message{
			ID:     fmt.Sprintf("cron-%d", time.Now().Unix()),
			ChatID: log.ChatID,
			UserID: userID,
			Text:   promptText,
			Source: log.Source, // "telegram" or "whatsapp"
		}

		// Run in a separate goroutine so one user doesn't block another
		go func(m models.Message) {
			// Create a background context specifically for this broadcast
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			slog.Info("sending proactive message", "user_id", m.UserID)
			if err := e.ProcessMessage(bgCtx, m); err != nil {
				slog.Error("failed to process proactive message", "user", m.UserID, "error", err)
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
		log, err := e.db.GetLastAuditSessionForUser(reminder.UserID)
		if err != nil || log == nil {
			slog.Warn("failed to get last audit log for reminder", "user_id", reminder.UserID, "error", err)
			continue
		}

		if log.Source == "" {
			continue
		}

		msg := models.Message{
			ID:     fmt.Sprintf("reminder-%d", reminder.ID),
			ChatID: log.ChatID,
			UserID: reminder.UserID,
			Source: log.Source,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)

		formattedMsg := fmt.Sprintf("🔔 *Reminder:*\n\n%s", reminder.Message)

		err = e.sendMessage(ctx, msg, formattedMsg)
		if err != nil {
			slog.Error("failed to send reminder message", "reminder_id", reminder.ID, "error", err)
			cancel()
			continue
		}
		cancel()

		_ = e.db.MarkReminderSent(reminder.ID)

		// Optionally log to history
		if e.store != nil {
			_ = e.store.AppendChatHistory(context.Background(), reminder.UserID, models.ChatMessage{Role: "assistant", Content: formattedMsg})
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

		log, err := e.db.GetLastAuditSessionForUser(targetUserID)
		if err != nil || log == nil || log.Source == "" {
			slog.Warn("failed to get last audit log for shared reminder", "user_id", targetUserID, "shared_reminder_id", s.ID)
			continue
		}

		msg := models.Message{
			ID:     fmt.Sprintf("shared-reminder-%d", s.ID),
			ChatID: log.ChatID,
			UserID: targetUserID,
			Source: log.Source,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		formattedMsg := fmt.Sprintf("🔔 *Shared Task Reminder:*\n\n**%s** (Topic: %s)", s.Title, s.Topic)
		err = e.sendMessage(ctx, msg, formattedMsg)
		if err != nil {
			slog.Error("failed to send shared reminder message", "shared_reminder_id", s.ID, "error", err)
			cancel()
			continue
		}
		cancel()

		_ = e.db.MarkSharedReminderSent(s.ID)
		if e.store != nil {
			_ = e.store.AppendChatHistory(context.Background(), targetUserID, models.ChatMessage{Role: "assistant", Content: formattedMsg})
		}
	}
}

func (e *Engine) handleUndo(ctx context.Context, user *models.User, msg models.Message, mutationID string) error {
	actorID := msg.UserID
	familyGroup := ""
	if user != nil {
		if strings.TrimSpace(user.PlatformID) != "" {
			actorID = strings.TrimSpace(user.PlatformID)
		}
		familyGroup = strings.TrimSpace(user.FamilyGroup)
	}

	if e.toolRegistry != nil {
		if mod, ok := e.toolRegistry.GetModule("durability"); ok {
			if rEngine, ok := mod.(RollbackEngine); ok {
				res, err := rEngine.Rollback(ctx, actorID, familyGroup, mutationID)
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
