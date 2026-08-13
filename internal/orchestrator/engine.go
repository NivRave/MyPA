package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer *broker.Consumer
	store    *state.Store
	llm      *llm.Client
	tg       *telegram.Client
	oauth    *calendar.OAuthConfig
	timezone string
}

// NewEngine initializes the orchestrator engine.
func NewEngine(consumer *broker.Consumer, store *state.Store, llm *llm.Client, tg *telegram.Client, oauth *calendar.OAuthConfig, timezone string) *Engine {
	return &Engine{
		consumer: consumer,
		store:    store,
		llm:      llm,
		tg:       tg,
		oauth:    oauth,
		timezone: timezone,
	}
}

// Start begins consuming messages and processing them.
func (e *Engine) Start(ctx context.Context) error {
	slog.Info("orchestrator engine starting")
	
	return e.consumer.Consume(func(msg models.Message) error {
		// Use a bounded timeout for each message processing to prevent stuck consumers
		msgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := e.processMessage(msgCtx, msg); err != nil {
			slog.Error("failed to process message", "message_id", msg.ID, "error", err)
			
			// Try to notify the user of the error
			_ = e.tg.SendMessage(msgCtx, msg.ChatID, "⚠️ Sorry, I encountered an internal error while processing your request.")
			
			// We return nil here so the broker ACKs the message. 
			// We don't want poison pills to crash the consumer loop indefinitely.
			return nil 
		}
		
		return nil
	})
}

func (e *Engine) processMessage(ctx context.Context, msg models.Message) error {
	slog.Info("processing message", "user_id", msg.UserID, "text", msg.Text)

	// Check for commands
	if msg.Text == "/connect" {
		url := e.oauth.AuthCodeURL(msg.UserID)
		reply := fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
		return e.tg.SendMessage(ctx, msg.ChatID, reply)
	}

	// 1. Check if there's a pending action waiting for confirmation
	pending, err := e.store.GetPendingAction(ctx, msg.UserID)
	if err != nil {
		return fmt.Errorf("failed to check pending action: %w", err)
	}

	if pending != nil {
		// Is the user confirming? (Simple naive check for now, can be LLM-powered later)
		text := msg.Text
		isConfirm := text == "yes" || text == "Yes" || text == "y" || text == "confirm" || text == "do it"
		isCancel := text == "no" || text == "No" || text == "n" || text == "cancel" || text == "stop"

		if isCancel {
			_ = e.store.ClearPendingAction(ctx, msg.UserID)
			_ = e.tg.SendMessage(ctx, msg.ChatID, "❌ Action canceled.")
			return nil
		}

		if isConfirm {
			_ = e.tg.SendMessage(ctx, msg.ChatID, "⏳ Creating event...")
			
			// Grab OAuth token
			tokenBytes, err := e.store.GetOAuthToken(ctx, msg.UserID)
			if err != nil || tokenBytes == nil {
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				return e.tg.SendMessage(ctx, msg.ChatID, "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.")
			}

			token, err := calendar.DecodeToken(tokenBytes)
			if err != nil {
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				return e.tg.SendMessage(ctx, msg.ChatID, "⚠️ Your Google connection expired or is invalid. Please send /connect again.")
			}

			// Create Calendar Client
			ts := e.oauth.TokenSource(ctx, token)
			calClient, err := calendar.NewClient(ctx, ts)
			if err != nil {
				return fmt.Errorf("failed to create calendar client: %w", err)
			}

			// Execute Action
			if pending.Action == "create_calendar_event" {
				link, err := calClient.CreateEvent(ctx, pending.Event)
				if err != nil {
					_ = e.tg.SendMessage(ctx, msg.ChatID, "❌ Failed to create event: "+err.Error())
					return fmt.Errorf("failed to create event: %w", err)
				}
				
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				successMsg := fmt.Sprintf("✅ **Event Created Successfully!**\n\n[View Event](%s)", link)
				return e.tg.SendMessage(ctx, msg.ChatID, successMsg)
			}
		}

		// If it's not a clear yes/no, we can clear the action and process it as a normal message,
		// or just tell them to confirm or cancel. Let's force confirm/cancel for safety.
		return e.tg.SendMessage(ctx, msg.ChatID, "⚠️ You have a pending action. Please reply 'yes' to confirm, or 'cancel' to abort.")
	}

	// 2. Fetch conversation history
	history, err := e.store.GetChatHistory(ctx, msg.UserID)
	if err != nil {
		return fmt.Errorf("failed to get chat history: %w", err)
	}

	// 3. Build the system prompt
	systemPrompt := fmt.Sprintf(
		"You are an omnipresent personal assistant. Your job is to help the user manage their time and tasks. "+
		"The current date and time is %s. The user's timezone is %s (assume this timezone for relative dates). "+
		"If the user asks to schedule a meeting, block time, or create an event, you MUST use the create_calendar_event tool. "+
		"Do not ask for confirmation if they provided enough details (title, start, end).",
		time.Now().Format(time.RFC1123), e.timezone,
	)

	// 4. Call LLM
	resp, err := e.llm.Chat(ctx, systemPrompt, history, msg.Text)
	if err != nil {
		return fmt.Errorf("llm chat failed: %w", err)
	}

	// 5. Handle LLM Response
	var replyText string

	if resp.ToolCall != nil {
		slog.Info("llm requested tool call", "tool", resp.ToolCall.Name)
		
		if resp.ToolCall.Name == "create_calendar_event" {
			// Extract args
			argsJSON, _ := json.Marshal(resp.ToolCall.Args)
			var event models.CalendarEvent
			if err := json.Unmarshal(argsJSON, &event); err != nil {
				return fmt.Errorf("failed to parse tool arguments into event: %w", err)
			}

			// Store Pending Action
			pendingAction := models.PendingAction{
				UserID:    msg.UserID,
				ChatID:    msg.ChatID,
				Action:    "create_calendar_event",
				Event:     event,
				CreatedAt: time.Now(),
			}

			if err := e.store.SetPendingAction(ctx, msg.UserID, pendingAction); err != nil {
				return fmt.Errorf("failed to save pending action: %w", err)
			}

			replyText = fmt.Sprintf("🗓️ **Proposal:** I will create an event titled '%s' from %s to %s.\n\nReply **yes** to confirm or **cancel** to abort.", 
				event.Title, event.StartTime, event.EndTime)
		} else {
			replyText = "⚠️ LLM tried to call an unknown tool."
		}
	} else {
		// Normal text response
		replyText = resp.Text
	}

	// 6. Send reply to Telegram
	if err := e.tg.SendMessage(ctx, msg.ChatID, replyText); err != nil {
		return fmt.Errorf("failed to send telegram message: %w", err)
	}

	// 7. Update conversation history
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "user", Content: msg.Text})
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "assistant", Content: replyText})

	return nil
}
