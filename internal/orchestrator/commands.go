package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// commandHandler handles a slash command. It sends its own reply and returns the text to record
// as the turn's final response (empty when nothing should be recorded) plus the send error.
type commandHandler func(e *Engine, ctx context.Context, user *models.User, msg channel.Inbound) (response string, err error)

// commands maps exact slash commands to their handlers (P4-1). "/undo <id>" is matched by prefix
// in dispatchCommand.
var commands = map[string]commandHandler{
	"/help":    (*Engine).cmdHelp,
	"/connect": (*Engine).cmdConnect,
	"/backup":  (*Engine).cmdBackup,
	"/restore": (*Engine).cmdRestore,
	"/undo":    (*Engine).cmdUndo,
	"/doctor":  (*Engine).cmdDoctor,
	// V4-32: people and their channel accounts (link.go).
	"/link":     (*Engine).cmdLinkIssue,
	"/accounts": (*Engine).cmdAccounts,
	"/unlink":   (*Engine).cmdUnlink,
	"/notify":   (*Engine).cmdNotify,
}

// prefixCommands are the commands that take an argument ("/undo <id>", "/link CODE", ...).
var prefixCommands = []string{"/undo", "/unlink", "/notify"}

// dispatchCommand runs the slash command in msg, if any. handled=false means msg is not a command.
func (e *Engine) dispatchCommand(ctx context.Context, user *models.User, msg channel.Inbound) (response string, handled bool, err error) {
	h, ok := commands[msg.Text]
	if !ok {
		if code, isLink := linkCodeArg(msg.Text); isLink {
			// "/link CODE" from an account that already is a person: the merge path (V4-32).
			response, err = e.redeemLinkCode(ctx, user, msg, code)
			return response, true, err
		}
		for _, p := range prefixCommands {
			if strings.HasPrefix(msg.Text, p+" ") {
				h, ok = commands[p], true
				break
			}
		}
	}
	if !ok {
		return "", false, nil
	}
	response, err = h(e, ctx, user, msg)
	return response, true, err
}

// handleConfirmation redeems a staged action ("CONFIRM [TOOL]"). handled=false means msg is not
// a confirmation.
func (e *Engine) handleConfirmation(ctx context.Context, user *models.User, msg channel.Inbound) (response string, handled bool, err error) {
	trimmedText := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(strings.ToUpper(trimmedText), "CONFIRM") || e.store == nil {
		return "", false, nil
	}
	parts := strings.Fields(trimmedText)
	expectedTool := ""
	if len(parts) >= 2 {
		expectedTool = strings.ToLower(parts[1])
	}

	// An approval belongs to the person and the conversation it was raised in (V4-32).
	key := approvalKey(user, msg)
	pending, err := e.store.GetAndClearPendingMetaTool(ctx, key)
	if err != nil {
		slog.Error("failed to retrieve pending meta tool action", "error", err)
		return "", true, e.sendMessage(ctx, msg, "❌ Error retrieving pending confirmation. Please try again.")
	}
	if pending != nil && strings.EqualFold(pending.ToolName, legacyRestoreActionName) {
		// BL-21: a restore staged by the retired chat restore before the upgrade. It is removed
		// (GetAndClear above) and treated as if nothing were pending; it never runs.
		slog.Info("dropped a restore staged before the chat restore was retired", "user_id", msg.Sender)
		pending = nil
	}
	if pending == nil {
		return "", true, e.sendMessage(ctx, msg, "⚠️ No pending confirmation found or it has expired.")
	}
	if expectedTool != "" && strings.ToLower(pending.ToolName) != expectedTool {
		_ = e.store.StagePendingMetaTool(ctx, key, *pending, 5*time.Minute)
		return "", true, e.sendMessage(ctx, msg, fmt.Sprintf("⚠️ Confirmation mismatch. Pending action is '%s', but you confirmed '%s'.", pending.ToolName, expectedTool))
	}
	slog.Info("executing confirmed meta tool action", "user_id", msg.Sender, "tool", pending.ToolName)
	call := &genai.FunctionCall{
		Name: pending.ToolName,
		Args: pending.Args,
	}
	resultStr := e.executeSingleTool(ctx, msg, nil, "", call)
	response = fmt.Sprintf("✅ **Confirmed and Executed:** `%s`\n\n%s", pending.ToolName, resultStr)
	return response, true, e.sendMessage(ctx, msg, response)
}

func (e *Engine) cmdHelp(ctx context.Context, _ *models.User, msg channel.Inbound) (string, error) {
	var sb strings.Builder
	sb.WriteString("🤖 *MyPA Assistant Help*\n\n")
	sb.WriteString("*System Commands:*\n")
	sb.WriteString("• `/connect` - Connect Google Account (Calendar, Tasks, Gmail, Contacts)\n")
	sb.WriteString("• `/backup` - Create a manual database backup\n")
	sb.WriteString("• `/restore` - Show how to restore the newest backup from the terminal; restores nothing itself (admins only)\n")
	sb.WriteString("• `/undo` - Revert your most recent action or specify mutation ID\n")
	sb.WriteString("• `/doctor` - Check the health of this install: channels, Google sign-ins, queues, backups, database (admins only)\n")
	sb.WriteString("• `/link` - Add another app (account) to you, with the same data; `/accounts` lists them\n")
	sb.WriteString("• `/notify N` - Send notifications to your account N; `/unlink N` removes account N\n")
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

func (e *Engine) cmdUndo(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	markSideEffect(ctx) // a rollback may be applied; never re-run it
	mutID := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/undo"))
	return "", e.handleUndo(ctx, user, msg, mutID)
}

func (e *Engine) cmdConnect(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	// The sign-in is stored under the person's key, so every account of the person finds it
	// (V4-32); the confirmation goes to where they want notifications.
	owner := msg.Sender
	if user != nil && strings.TrimSpace(user.PlatformID) != "" {
		owner = strings.TrimSpace(user.PlatformID)
	}
	nonce := ""
	if e.store != nil {
		nonceBytes := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, nonceBytes); err == nil {
			nonce = hex.EncodeToString(nonceBytes)
			_ = e.store.SaveOAuthState(ctx, nonce, owner, 10*time.Minute)
		}
	}
	stateParam := nonce
	if stateParam == "" {
		stateParam = owner
	}
	url := e.oauthCfg.AuthCodeURL(stateParam)
	response := fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
	return response, e.sendMessage(ctx, msg, response)
}

func (e *Engine) cmdBackup(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !e.scopeOf(ctx, user).Can(identity.RunBackup) {
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

func (e *Engine) cmdRestore(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	// BL-21: replies with the steps for `transfer import --replace`; never restores from chat.
	return "", e.handleRestoreCommand(ctx, user, msg)
}
