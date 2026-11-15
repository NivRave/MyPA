package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// commandHandler handles a slash command. It sends its own reply and returns the text to record
// as the turn's final response (empty when nothing should be recorded) plus the send error.
type commandHandler func(e *Engine, ctx context.Context, user *models.User, msg models.Message) (response string, err error)

// commands maps exact slash commands to their handlers (P4-1). "/undo <id>" is matched by prefix
// in dispatchCommand.
var commands = map[string]commandHandler{
	"/help":    (*Engine).cmdHelp,
	"/connect": (*Engine).cmdConnect,
	"/backup":  (*Engine).cmdBackup,
	"/restore": (*Engine).cmdRestore,
	"/undo":    (*Engine).cmdUndo,
}

// dispatchCommand runs the slash command in msg, if any. handled=false means msg is not a command.
func (e *Engine) dispatchCommand(ctx context.Context, user *models.User, msg models.Message) (response string, handled bool, err error) {
	h, ok := commands[msg.Text]
	if !ok && strings.HasPrefix(msg.Text, "/undo ") {
		h, ok = commands["/undo"], true
	}
	if !ok {
		return "", false, nil
	}
	response, err = h(e, ctx, user, msg)
	return response, true, err
}

// handleConfirmation redeems a staged action ("CONFIRM [TOOL]"). handled=false means msg is not
// a confirmation.
func (e *Engine) handleConfirmation(ctx context.Context, user *models.User, msg models.Message) (response string, handled bool, err error) {
	trimmedText := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(strings.ToUpper(trimmedText), "CONFIRM") || e.store == nil {
		return "", false, nil
	}
	parts := strings.Fields(trimmedText)
	expectedTool := ""
	if len(parts) >= 2 {
		expectedTool = strings.ToLower(parts[1])
	}

	pending, err := e.store.GetAndClearPendingMetaTool(ctx, msg.UserID)
	if err != nil {
		slog.Error("failed to retrieve pending meta tool action", "error", err)
		return "", true, e.sendMessage(ctx, msg, "❌ Error retrieving pending confirmation. Please try again.")
	}
	if pending == nil {
		return "", true, e.sendMessage(ctx, msg, "⚠️ No pending confirmation found or it has expired.")
	}
	if expectedTool != "" && strings.ToLower(pending.ToolName) != expectedTool {
		_ = e.store.StagePendingMetaTool(ctx, msg.UserID, *pending, 5*time.Minute)
		return "", true, e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ Confirmation mismatch. Pending action is '%s', but you confirmed '%s'.", pending.ToolName, expectedTool))
	}
	if isDestructiveAction(pending.ToolName) {
		if expectedTool == "" {
			// Destructive actions require the action name; keep the pending action alive.
			remaining := restoreConfirmTTL - time.Since(pending.CreatedAt)
			if remaining > 0 {
				_ = e.store.StagePendingMetaTool(ctx, msg.UserID, *pending, remaining)
			}
			return "", true, e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ This is a destructive action. Reply `CONFIRM %s` to proceed.", strings.ToUpper(pending.ToolName)))
		}
		return "", true, e.executeConfirmedRestore(ctx, user, msg, pending)
	}

	slog.Info("executing confirmed meta tool action", "user_id", msg.UserID, "tool", pending.ToolName)
	call := &genai.FunctionCall{
		Name: pending.ToolName,
		Args: pending.Args,
	}
	resultStr := e.executeSingleTool(ctx, msg, nil, "", call)
	response = fmt.Sprintf("✅ **Confirmed and Executed:** `%s`\n\n%s", pending.ToolName, resultStr)
	return response, true, e.sendMessage(ctx, msg, response)
}

func (e *Engine) cmdHelp(ctx context.Context, _ *models.User, msg models.Message) (string, error) {
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

	response := sb.String()
	return response, e.sendMessage(ctx, msg, response)
}

func (e *Engine) cmdUndo(ctx context.Context, user *models.User, msg models.Message) (string, error) {
	markSideEffect(ctx) // a rollback may be applied; never re-run it
	mutID := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/undo"))
	return "", e.handleUndo(ctx, user, msg, mutID)
}

func (e *Engine) cmdConnect(ctx context.Context, _ *models.User, msg models.Message) (string, error) {
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
	response := fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
	return response, e.sendMessage(ctx, msg, response)
}

func (e *Engine) cmdBackup(ctx context.Context, user *models.User, msg models.Message) (string, error) {
	if user.Role != "admin" {
		slog.Warn("unauthorized backup command attempt", "user_id", user.PlatformID, "role", user.Role)
		return "", e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
	}
	_ = e.sendMessage(ctx, msg, "🔄 Starting database backup...")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return "", e.sendMessage(ctx, msg, "❌ Backup failed: DATABASE_URL not set.")
	}
	markSideEffect(ctx)
	ops := e.backups.withDefaults()
	path, err := ops.backup(databaseURL, ops.dir) // encrypted + GFS retention (P3-4)
	if err != nil {
		slog.Error("Manual backup failed", "error", err)
		return "", e.sendMessage(ctx, msg, fmt.Sprintf("❌ Backup failed: %v", err))
	}
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("✅ Backup completed successfully!\n📁 Path: %s", path))
}

func (e *Engine) cmdRestore(ctx context.Context, user *models.User, msg models.Message) (string, error) {
	// P1-5: never restore immediately — stage it and require "CONFIRM RESTORE".
	return "", e.handleRestoreCommand(ctx, user, msg)
}
