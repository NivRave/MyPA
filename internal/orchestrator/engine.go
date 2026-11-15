package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/markdown"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"

	"google.golang.org/genai"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer        *broker.Consumer
	store           *state.Store
	backups         backupOps // pg_dump/pg_restore seam; zero value = real db functions
	db              DBClient
	llm             LLMClient
	tgClient        TelegramClient
	twClient        TwilioClient
	audioClient     AudioClient
	oauthCfg        *calendar.OAuthConfig
	gmailClient     GmailClient
	tasksClient     TasksClient
	contactsClient  ContactsClient
	tavilyClient    TavilyClient
	calendarFactory func(context.Context, string) (CalendarClient, error)
	timezone        string
	publisher       EventPublisher
	wg              sync.WaitGroup
	onWorkflowsChanged func()
	toolRegistry    *Registry
	eventBus        *events.EventBus
	intentMatcher   IntentMatcher
	calendar        CalendarConventions // week start / weekend for the system prompt (P2-6)
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
		if toolSetter, ok := e.llm.(interface{ SetExtraTools([]*genai.FunctionDeclaration) }); ok {
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
		consumer:        cons,
		store:           store,
		db:              db,
		llm:             llm,
		tgClient:        tg,
		twClient:        tw,
		oauthCfg:        oauth,
		gmailClient:     gmailC,
		tasksClient:     tasksC,
		contactsClient:  contactsC,
		tavilyClient:    tavilyC,
		audioClient:     audio,
		publisher:       pub,
		timezone:        tz,
		intentMatcher:   NewKeywordIntentMatcher(),
		calendar:        DefaultCalendarConventions(),
	}

	e.calendarFactory = func(ctx context.Context, userID string) (CalendarClient, error) {
		tokenBytes, err := e.store.GetOAuthToken(ctx, userID)
		if err != nil || tokenBytes == nil {
			return nil, fmt.Errorf("unauthorized")
		}

		var tok oauth2.Token
		if err := json.Unmarshal(tokenBytes, &tok); err != nil {
			return nil, fmt.Errorf("unauthorized")
		}

		ts := e.oauthCfg.TokenSource(ctx, &tok)
		return calendar.NewClient(ctx, ts)
	}

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

	slog.Info("processing message", "user_id", msg.UserID, "text", msg.Text)

	var llmResponse string
	sessionID := uuid.New().String()
	startTime := time.Now()

	defer func() {
		e.wg.Add(1)
		// Publish the telemetry session asynchronously
		go func(msgText string, errStr string) {
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
		}())
	}()

	secState := &TurnSecurityState{}
	ctx = WithTurnSecurityState(ctx, secState)

	// Runs before the telemetry defer above (LIFO), so telemetry sees the final error.
	effects := &turnEffects{}
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

	// Interactive confirmation gate redemption for staged privileged actions
	trimmedText := strings.TrimSpace(msg.Text)
	if strings.HasPrefix(strings.ToUpper(trimmedText), "CONFIRM") {
		parts := strings.Fields(trimmedText)
		expectedTool := ""
		if len(parts) >= 2 {
			expectedTool = strings.ToLower(parts[1])
		}
		if e.store != nil {
			pending, err := e.store.GetAndClearPendingMetaTool(ctx, msg.UserID)
			if err != nil {
				slog.Error("failed to retrieve pending meta tool action", "error", err)
				return e.sendMessage(ctx, msg, "❌ Error retrieving pending confirmation. Please try again.")
			}
			if pending == nil {
				return e.sendMessage(ctx, msg, "⚠️ No pending confirmation found or it has expired.")
			}
			if expectedTool != "" && strings.ToLower(pending.ToolName) != expectedTool {
				_ = e.store.StagePendingMetaTool(ctx, msg.UserID, *pending, 5*time.Minute)
				return e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ Confirmation mismatch. Pending action is '%s', but you confirmed '%s'.", pending.ToolName, expectedTool))
			}
			if isDestructiveAction(pending.ToolName) {
				if expectedTool == "" {
					// Destructive actions require the action name; keep the pending action alive.
					remaining := restoreConfirmTTL - time.Since(pending.CreatedAt)
					if remaining > 0 {
						_ = e.store.StagePendingMetaTool(ctx, msg.UserID, *pending, remaining)
					}
					return e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ This is a destructive action. Reply `CONFIRM %s` to proceed.", strings.ToUpper(pending.ToolName)))
				}
				return e.executeConfirmedRestore(ctx, user, msg, pending)
			}

			slog.Info("executing confirmed meta tool action", "user_id", msg.UserID, "tool", pending.ToolName)
			call := &genai.FunctionCall{
				Name: pending.ToolName,
				Args: pending.Args,
			}
			resultStr := e.executeSingleTool(ctx, msg, nil, "", call)
			llmResponse = fmt.Sprintf("✅ **Confirmed and Executed:** `%s`\n\n%s", pending.ToolName, resultStr)
			return e.sendMessage(ctx, msg, llmResponse)
		}
	}

	// Check for commands
	if msg.Text == "/help" {
		var sb strings.Builder
		sb.WriteString("🤖 *MyPA Assistant Help*\n\n")
		sb.WriteString("*System Commands:*\n")
		sb.WriteString("• `/connect` - Connect Google Account (Calendar, Tasks, Gmail, Contacts)\n")
		sb.WriteString("• `/backup` - Create a manual database backup\n")
		sb.WriteString("• `/restore` - Restore the database from the latest backup (asks for `CONFIRM RESTORE`; takes a safety backup first)\n")
		sb.WriteString("• `/undo` - Revert your most recent action or specify mutation ID\n")
		sb.WriteString("• `/help` - Show this help message\n\n")
		sb.WriteString("*AI Capabilities:*\n")
		sb.WriteString("I am an AI assistant. You can speak to me naturally. Here are my underlying capabilities:\n\n")
		
		for _, fn := range llm.CalendarEventTool.FunctionDeclarations {
			desc := fn.Description
			if idx := strings.Index(desc, " Call this"); idx != -1 {
				desc = desc[:idx]
			} else if idx := strings.Index(desc, ". Use this"); idx != -1 {
				desc = desc[:idx+1]
			} else if idx := strings.Index(desc, ". Does not"); idx != -1 {
				desc = desc[:idx+1]
			}
			
			name := strings.ReplaceAll(fn.Name, "_", " ")
			sb.WriteString(fmt.Sprintf("• *%s*: %s\n", name, desc))
		}
		
		sb.WriteString("\n*Examples of what you can ask:*\n")
		sb.WriteString("🗣️ _\"Remind me to call Mom tomorrow at 5pm\"_\n")
		sb.WriteString("🗣️ _\"What's on my calendar for today?\"_\n")
		sb.WriteString("🗣️ _\"Do I have any unread emails?\"_\n")
		sb.WriteString("🗣️ _\"Add 'Buy milk' to my Groceries list\"_\n")
		sb.WriteString("🗣️ _\"Search the web for the weather in Tel Aviv\"_\n")
		
		llmResponse = sb.String()
		return e.sendMessage(ctx, msg, llmResponse)
	}

	if msg.Text == "/undo" || strings.HasPrefix(msg.Text, "/undo ") {
		markSideEffect(ctx) // a rollback may be applied; never re-run it
		mutID := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/undo"))
		return e.handleUndo(ctx, user, msg, mutID)
	}

	if msg.Text == "/connect" {
		nonce := ""
		if e.store != nil {
			nonceBytes := make([]byte, 32)
			if _, err := io.ReadFull(rand.Reader, nonceBytes); err == nil {
				nonce = hex.EncodeToString(nonceBytes)
				_ = e.store.SaveOAuthState(ctx, nonce, msg.UserID, 10*time.Minute)
			}
		}
		stateParam := nonce
		if stateParam == "" {
			stateParam = msg.UserID
		}
		url := e.oauthCfg.AuthCodeURL(stateParam)
		llmResponse = fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
		return e.sendMessage(ctx, msg, llmResponse)
	}

	if msg.Text == "/backup" {
		if user.Role != "admin" {
			slog.Warn("unauthorized backup command attempt", "user_id", user.PlatformID, "role", user.Role)
			return e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
		}
		_ = e.sendMessage(ctx, msg, "🔄 Starting database backup...")
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			return e.sendMessage(ctx, msg, "❌ Backup failed: DATABASE_URL not set.")
		}
		markSideEffect(ctx)
		path, err := db.BackupDatabase(databaseURL, "/backups", 3)
		if err != nil {
			slog.Error("Manual backup failed", "error", err)
			return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Backup failed: %v", err))
		}
		return e.sendMessage(ctx, msg, fmt.Sprintf("✅ Backup completed successfully!\n📁 Path: %s", path))
	}

	if msg.Text == "/restore" {
		// P1-5: never restore immediately — stage it and require "CONFIRM RESTORE".
		return e.handleRestoreCommand(ctx, user, msg)
	}

	var photoData []byte
	var photoMimeType string

	// 0. Intercept and transcribe Voice Messages and Photos
	if msg.PhotoFileID != "" {
		filePath, err := e.tgClient.GetFile(ctx, msg.PhotoFileID)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to retrieve photo metadata.")
			return fmt.Errorf("failed to get photo path: %w", err)
		}
		photoData, err = e.tgClient.DownloadFile(ctx, filePath)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to download photo.")
			return fmt.Errorf("failed to download photo: %w", err)
		}
		photoMimeType = "image/jpeg"
	} else if msg.VoiceFileID != "" {
		filePath, err := e.tgClient.GetFile(ctx, msg.VoiceFileID)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to retrieve voice message metadata.")
			return fmt.Errorf("failed to get file path: %w", err)
		}

		audioData, err := e.tgClient.DownloadFile(ctx, filePath)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to download voice message.")
			return fmt.Errorf("failed to download audio: %w", err)
		}

		transcribedText, err := e.audioClient.TranscribeAudio(ctx, audioData, "voice.ogg")
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to transcribe audio.")
			return fmt.Errorf("failed to transcribe: %w", err)
		}

		// Notify user of transcription
		_ = e.sendMessage(ctx, msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))
		msg.Text = transcribedText
	} else if msg.Source == "whatsapp" && msg.MediaURL != "" {
		mediaType := strings.ToLower(msg.MediaContentType)
		slog.Info("WhatsApp media received", "media_url", msg.MediaURL, "content_type", mediaType)

		isTextDoc := strings.HasPrefix(mediaType, "text/") ||
			mediaType == "application/json" ||
			mediaType == "application/xml" ||
			mediaType == "application/csv" ||
			mediaType == "application/x-yaml" ||
			mediaType == "text/yaml"

		if strings.HasPrefix(mediaType, "image/") {
			photoData, err = e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, msg, "❌ Failed to download WhatsApp photo.")
				return fmt.Errorf("failed to download twilio photo: %w", err)
			}
			photoMimeType = msg.MediaContentType
		} else if isTextDoc {
			docData, err := e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, msg, "❌ Failed to download WhatsApp document.")
				return fmt.Errorf("failed to download twilio document: %w", err)
			}
			docText := WrapUntrustedContent("whatsapp_document", msg.MediaURL, string(docData))
			secState.MarkUntrusted("whatsapp_document:" + msg.MediaURL)
			if strings.TrimSpace(msg.Text) != "" {
				msg.Text = fmt.Sprintf("%s\n\n[Attached Document Content]:\n%s", strings.TrimSpace(msg.Text), docText)
			} else {
				msg.Text = docText
			}
		} else if mediaType == "application/pdf" {
			photoData, err = e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, msg, "❌ Failed to download WhatsApp PDF.")
				return fmt.Errorf("failed to download twilio pdf: %w", err)
			}
			photoMimeType = "application/pdf"
			if strings.TrimSpace(msg.Text) == "" {
				msg.Text = "Please process and analyze the attached document."
			}
		} else if strings.HasPrefix(mediaType, "audio/") || mediaType == "application/ogg" {
			// Handle Twilio Voice Messages
			audioData, err := e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, msg, "❌ Failed to download WhatsApp voice message.")
				return fmt.Errorf("failed to download twilio media: %w", err)
			}

			transcribedText, err := e.audioClient.TranscribeAudio(ctx, audioData, "voice.ogg")
			if err != nil {
				_ = e.sendMessage(ctx, msg, "❌ Failed to transcribe WhatsApp audio.")
				return fmt.Errorf("failed to transcribe twilio media: %w", err)
			}

			_ = e.sendMessage(ctx, msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))
			msg.Text = transcribedText
		} else {
			_ = e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ Unsupported attachment format (%s). Please send a text file (.txt, .md, .csv), PDF, image, or voice note.", msg.MediaContentType))
			return nil
		}
	}

	// 2. Fetch conversation history
	history, err := e.store.GetChatHistory(ctx, msg.UserID)
	if err != nil {
		return fmt.Errorf("failed to get chat history: %w", err)
	}

	// 3. Select this turn's tools first: the prompt's tool guidance follows them (P2-6).
	// Dynamic Two-Tier Tool Context Selection (SCALE-01) + sticky follow-up domains (P2-2)
	var matchedDomains []DomainGroup
	var activeToolNames map[string]bool // nil = no registry → all guidance
	if e.toolRegistry != nil {
		if e.intentMatcher != nil {
			matchedDomains = e.intentMatcher.MatchDomains(msg.Text)
		}
		selected := unionDomains(matchedDomains, e.loadStickyDomains(ctx, msg.UserID))
		activeTools := e.toolRegistry.SelectDeclarations(selected, false)
		ctx = llm.WithActiveTools(ctx, activeTools)
		activeToolNames = make(map[string]bool, len(activeTools))
		for _, d := range activeTools {
			activeToolNames[d.Name] = true
		}
	}

	// Search long-term semantic memory — skipped for trivial acknowledgements (P2-5): an
	// embedding of "thanks!" retrieves noise and costs an API call per message.
	var memoryLines []string
	var embedding []float32
	if !isTrivialMessage(msg.Text) {
		embedding, err = e.llm.GenerateEmbedding(ctx, msg.Text)
	}
	if err == nil && len(embedding) > 0 {
		// P2-4: single memory store. Visibility (own personal + family-shared) is enforced by
		// the repository's tenant filter, so no per-member user ID list is needed.
		memories, searchErr := e.db.SearchSimilarMemories(ctx, msg.UserID, user.FamilyGroup, pgvector.NewVector(embedding), 5)
		if searchErr != nil {
			slog.Warn("memory retrieval failed; continuing without memories", "user_id", msg.UserID, "error", searchErr)
		}
		for _, m := range memories {
			scopeTag := ""
			if m.Scope == "family" {
				scopeTag = " (Family Shared)"
			}
			memoryLines = append(memoryLines, "- "+m.Fact+scopeTag)
		}
	}

	// Dynamic per-module system prompt sections (cached per user, P1-3)
	moduleSections := ""
	if e.toolRegistry != nil {
		callCtx := e.buildCallContext(ctx, user, msg)
		moduleSections = e.toolRegistry.CombinedSystemPrompt(ctx, callCtx)
	}

	// Build the system prompt, most-stable sections first (P2-6, see prompt.go)
	systemPrompt := buildSystemPrompt(promptInputs{
		UserName:       user.Name,
		Role:           user.Role,
		FamilyGroup:    user.FamilyGroup,
		ActiveTools:    activeToolNames,
		ModuleSections: moduleSections,
		Memories:       memoryLines,
		Now:            time.Now(),
		Location:       e.location(),
		Calendar:       e.calendar,
	})

	// 4. Call LLM
	promptText := msg.Text
	resp, err := e.llm.Chat(ctx, systemPrompt, history, promptText, photoData, photoMimeType)
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			normalized := NormalizeHebrewPrepositionNumbers(promptText)
			if normalized != promptText {
				slog.Info("retrying LLM chat with normalized prompt after content block", "original", promptText, "normalized", normalized, "reason", blockedErr.BlockReason)
				resp, err = e.llm.Chat(ctx, systemPrompt, history, normalized, photoData, photoMimeType)
			}
		}
	}
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			slog.Warn("llm prompt blocked by safety filter", "user_id", msg.UserID, "reason", blockedErr.BlockReason)
			replyText := "⚠️ The AI safety filter flagged this request (likely a false positive). Please try phrasing it slightly differently, for example by adding a hyphen (e.g. 'ב-18:00') or using full time/date words."
			llmResponse = replyText
			_ = e.sendMessage(ctx, msg, replyText)
			return nil
		}
		return fmt.Errorf("llm chat failed: %w", err)
	}

	// 5. Handle LLM Response
	var replyText string

	if len(resp.ToolCalls) > 0 {
		// Generate AuditEvent for tool call?
		// We can add this later, for now just skip assigning actionTaken
		
		// Initialize the loop history with the user's message
		loopHistory := append(history, models.ChatMessage{
			Role: "user", 
			Content: msg.Text,
			PhotoData: photoData,
			PhotoMimeType: photoMimeType,
		})
		
		replyText, _ = e.handleToolCalls(ctx, msg, loopHistory, systemPrompt, resp.ToolCalls, 1)
	} else {
		// No tool call
		replyText = resp.Text
	}

	// Remember what this turn used so a short follow-up ("yes, the second one") keeps its tools.
	e.saveStickyDomains(ctx, msg.UserID, matchedDomains, effects.calledTools())

	// 6. Send reply to Telegram
	if strings.TrimSpace(replyText) == "" {
		replyText = "⚠️ Sorry, I didn't get a response from the AI. Please try again."
	}
	llmResponse = replyText
	
	if err := e.sendMessage(ctx, msg, replyText); err != nil {
		return fmt.Errorf("failed to send reply message: %w", err)
	}
	markSideEffect(ctx) // reply delivered: a later failure must not re-send it

	// 7. Update conversation history (SCALE-02: strip PhotoData bytes)
	persistedContent := msg.Text
	if len(photoData) > 0 {
		marker := formatAttachmentMarker(photoMimeType, len(photoData))
		if strings.TrimSpace(persistedContent) == "" {
			persistedContent = marker
		} else {
			persistedContent = fmt.Sprintf("%s\n%s", strings.TrimSpace(persistedContent), marker)
		}
	}

	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{
		Role:    "user",
		Content: persistedContent,
	})
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "assistant", Content: replyText})

	return nil
}

// MaxToolRecursionDepth is the maximum number of recursive tool loops allowed per turn.
const MaxToolRecursionDepth = 8

// MaxConcurrentTools is the maximum number of tool calls executed in parallel within a single turn.
const MaxConcurrentTools = 8

const maxToolIterations = MaxToolRecursionDepth

// executeToolsConcurrently executes a batch of function calls in parallel using errgroup.
// It guarantees deterministic indexing, context cancellation, panic recovery, and bounded concurrency.
func (e *Engine) executeToolsConcurrently(
	ctx context.Context,
	msg models.Message,
	history []models.ChatMessage,
	systemPrompt string,
	toolCalls []*genai.FunctionCall,
) ([]models.FunctionCall, []models.FunctionResponse) {
	n := len(toolCalls)
	if n == 0 {
		return nil, nil
	}

	funcCalls := make([]models.FunctionCall, n)
	funcResponses := make([]models.FunctionResponse, n)

	for i, tc := range toolCalls {
		funcCalls[i] = models.FunctionCall{Name: tc.Name, Args: tc.Args}
	}

	if n == 1 {
		tc := toolCalls[0]
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic recovered during single tool execution",
						"tool", tc.Name,
						"panic", r,
						"user_id", msg.UserID,
					)
					funcResponses[0] = models.FunctionResponse{
						Name: tc.Name,
						Response: map[string]interface{}{
							"result": fmt.Sprintf("❌ Internal panic during tool execution: %v", r),
						},
					}
				}
			}()
			resultStr := e.executeSingleTool(ctx, msg, history, systemPrompt, tc)
			funcResponses[0] = models.FunctionResponse{
				Name:     tc.Name,
				Response: map[string]interface{}{"result": resultStr},
			}
		}()
		return funcCalls, funcResponses
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxConcurrentTools)

	for i, tc := range toolCalls {
		idx := i
		call := tc
		g.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic recovered during concurrent tool execution",
						"tool", call.Name,
						"panic", r,
						"user_id", msg.UserID,
					)
					funcResponses[idx] = models.FunctionResponse{
						Name: call.Name,
						Response: map[string]interface{}{
							"result": fmt.Sprintf("❌ Internal panic during tool execution: %v", r),
						},
					}
				}
			}()

			resultStr := e.executeSingleTool(gctx, msg, history, systemPrompt, call)
			funcResponses[idx] = models.FunctionResponse{
				Name:     call.Name,
				Response: map[string]interface{}{"result": resultStr},
			}
			return nil
		})
	}
	_ = g.Wait()

	return funcCalls, funcResponses
}

func (e *Engine) handleToolCalls(
	ctx context.Context,
	msg models.Message,
	history []models.ChatMessage,
	systemPrompt string,
	toolCalls []*genai.FunctionCall,
	iteration int,
) (string, error) {
	if iteration <= 0 {
		iteration = 1
	}

	funcCalls, funcResponses := e.executeToolsConcurrently(ctx, msg, history, systemPrompt, toolCalls)

	// Add the actual assistant tool calls natively
	extendedHistory := append(history, models.ChatMessage{
		Role:      "assistant",
		ToolCalls: funcCalls,
	})

	// Add the function responses natively
	extendedHistory = append(extendedHistory, models.ChatMessage{
		Role:          "function",
		ToolResponses: funcResponses,
	})

	// Check iteration limit (SCALE-03: bound to 8 iterations)
	if iteration >= maxToolIterations {
		slog.Warn("max tool recursion reached, requesting terminal summary without tools",
			"user_id", msg.UserID, "iteration", iteration)

		terminalPrompt := "Maximum tool execution limit reached (8 steps). Do NOT call any more tools. " +
			"Please provide a friendly, clear, and comprehensive final summary of all actions completed so far directly to the user."

		// Force no-tools call to guarantee conversational termination
		summaryResp, err := e.llm.Chat(llm.WithNoTools(ctx), systemPrompt, extendedHistory, terminalPrompt, nil, "")
		if err != nil {
			return "✅ Actions were executed (maximum 8 steps reached).", nil
		}
		if summaryResp != nil && summaryResp.Text != "" {
			return summaryResp.Text, nil
		}
		return "✅ All requested actions have been completed up to the maximum iteration limit.", nil
	}

	// Normal continuation prompt
	prompt := "Please continue fulfilling the user's request. What's next? If you are completely done, provide a friendly, nicely formatted final summary of all your actions directly to the user."

	summaryResp, err := e.llm.Chat(ctx, systemPrompt, extendedHistory, prompt, nil, "")
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			return "⚠️ Actions were executed, but the final response summary was flagged by the AI content filter.", nil
		}
		return "❌ Failed to process: " + err.Error(), nil
	}

	if summaryResp != nil && len(summaryResp.ToolCalls) > 0 {
		extendedHistory = append(extendedHistory, models.ChatMessage{Role: "user", Content: prompt})
		return e.handleToolCalls(ctx, msg, extendedHistory, systemPrompt, summaryResp.ToolCalls, iteration+1)
	}

	if summaryResp != nil {
		return summaryResp.Text, nil
	}
	return "", nil
}

func (e *Engine) handleToolCall(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) (string, error) {
	if toolCall.Name == "schedule_reminder" || toolCall.Name == "unknown_tool" || toolCall.Name == "fetch_webpage" {
		return e.executeSingleTool(ctx, msg, history, systemPrompt, toolCall), nil
	}
	if toolCall.Name == "search_emails" {
		res := e.executeSingleTool(ctx, msg, history, systemPrompt, toolCall)
		if res == "You have no unread emails." {
			return res, nil
		}
	}
	return e.handleToolCalls(ctx, msg, history, systemPrompt, []*genai.FunctionCall{toolCall}, 1)
}

func (e *Engine) executeSingleTool(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) string {
	slog.Info("llm requested tool call", "tool", toolCall.Name)

	isMetaTool := toolCall.Name == "update_user_preferences" ||
		toolCall.Name == "create_automation_rule" ||
		toolCall.Name == "schedule_workflow" ||
		toolCall.Name == "register_external_api"

	if isMetaTool {
		secState := GetTurnSecurityState(ctx)
		if secState != nil {
			isUntrusted, sources := secState.IsUntrusted()
			if isUntrusted {
				if e.store != nil {
					stageData := state.PendingMetaToolAction{
						ToolName:  toolCall.Name,
						Args:      toolCall.Args,
						UserID:    msg.UserID,
						CreatedAt: time.Now(),
					}
					_ = e.store.StagePendingMetaTool(ctx, msg.UserID, stageData, 5*time.Minute)
				}

				sourceDesc := "external untrusted content"
				if len(sources) > 0 {
					sourceDesc = strings.Join(sources, ", ")
				}

				return fmt.Sprintf(
					"🔒 **Interactive Security Confirmation Required**\n\n"+
						"The assistant received a command to perform a privileged system action (`%s`) while processing external data (%s).\n"+
						"To prevent indirect prompt injection, this modification was NOT applied automatically.\n\n"+
						"👉 If you intended to make this change, please reply with:\n`CONFIRM %s`",
					toolCall.Name, sourceDesc, strings.ToUpper(toolCall.Name),
				)
			}
		}
	}
	
	// Delegate to modular ToolRegistry if registered
	if e.toolRegistry != nil {
		// P2-7: reuse the user resolved at the start of the turn; look it up only for callers
		// outside a turn (direct invocations, tests).
		user := turnUser(ctx)
		if user == nil && e.db != nil {
			user, _ = e.db.GetUser(msg.UserID)
		}
		callCtx := e.buildCallContext(ctx, user, msg)
		res, handled, err := e.toolRegistry.Execute(ctx, callCtx, toolCall.Name, toolCall.Args)
		if handled {
			recordToolCall(ctx, toolCall.Name)
			// The tool ran (possibly partially, even on error): retrying the turn would duplicate it.
			// Read-only built-ins computed locally are exempt.
			if !readOnlyBuiltinTools[toolCall.Name] {
				markSideEffect(ctx)
			}
			if err != nil {
				slog.Error("registry tool execution failed", "tool", toolCall.Name, "error", err)
				return fmt.Sprintf("❌ Error executing %s: %v", toolCall.Name, err)
			}
			return res
		}
	}

	if toolCall.Name == "get_current_time" {
		loc := time.UTC
		if strings.TrimSpace(e.timezone) != "" {
			if l, err := time.LoadLocation(strings.TrimSpace(e.timezone)); err == nil {
				loc = l
			}
		}
		if toolCall.Args != nil {
			if tzVal, ok := toolCall.Args["timezone"].(string); ok && strings.TrimSpace(tzVal) != "" {
				if l, err := time.LoadLocation(strings.TrimSpace(tzVal)); err == nil {
					loc = l
				}
			}
		}
		now := time.Now().In(loc)
		resp := map[string]interface{}{
			"current_time": now.Format(time.RFC3339),
			"timezone":     loc.String(),
			"day_of_week":  now.Weekday().String(),
			"unix":         now.Unix(),
		}
		b, _ := json.Marshal(resp)
		return string(b)
	}

	return "⚠️ LLM tried to call an unknown tool."
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

