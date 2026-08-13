package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/audio"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"

	"google.golang.org/genai"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer *broker.Consumer
	store    *state.Store
	db       *db.Client
	llm      *llm.Client
	tg       *telegram.Client
	tw       *twilio.Client
	oauth    *calendar.OAuthConfig
	audio    *audio.Client
	timezone string
}

// NewEngine initializes the orchestrator engine.
func NewEngine(consumer *broker.Consumer, store *state.Store, dbClient *db.Client, llm *llm.Client, tg *telegram.Client, tw *twilio.Client, oauth *calendar.OAuthConfig, audioClient *audio.Client, timezone string) *Engine {
	return &Engine{
		consumer: consumer,
		store:    store,
		db:       dbClient,
		llm:      llm,
		tg:       tg,
		tw:       tw,
		oauth:    oauth,
		audio:    audioClient,
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
			_ = e.sendMessage(msgCtx, msg, "⚠️ Sorry, I encountered an internal error while processing your request.")
			
			// We return nil here so the broker ACKs the message. 
			// We don't want poison pills to crash the consumer loop indefinitely.
			return nil 
		}
		
		return nil
	})
}

func (e *Engine) sendMessage(ctx context.Context, msg models.Message, text string) error {
	if msg.Source == "whatsapp" {
		return e.tw.SendMessage(ctx, msg.UserID, text)
	}
	return e.tg.SendMessage(ctx, msg.ChatID, text)
}

func (e *Engine) processMessage(ctx context.Context, msg models.Message) error {
	slog.Info("processing message", "user_id", msg.UserID, "text", msg.Text)

	var actionTaken string
	var llmResponse string

	defer func() {
		// Log the interaction asynchronously
		go func(msgText string) {
			err := e.db.LogInteraction(models.AuditLog{
				UserID:      msg.UserID,
				ChatID:      msg.ChatID,
				Source:      msg.Source,
				UserMessage: msgText,
				LLMResponse: llmResponse,
				ActionTaken: actionTaken,
				CreatedAt:   time.Now(),
			})
			if err != nil {
				slog.Error("failed to save audit log", "error", err)
			}
		}(msg.Text) // Pass msg.Text in case it gets mutated (like voice transcription)
	}()

	// Check for commands
	if msg.Text == "/connect" {
		url := e.oauth.AuthCodeURL(msg.UserID)
		llmResponse = fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
		actionTaken = "connect_command"
		return e.sendMessage(ctx, msg, llmResponse)
	}

	// 0. Intercept and transcribe Voice Messages
	if msg.VoiceFileID != "" {
		filePath, err := e.tg.GetFile(ctx, msg.VoiceFileID)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to retrieve voice message metadata.")
			return fmt.Errorf("failed to get file path: %w", err)
		}

		audioData, err := e.tg.DownloadFile(ctx, filePath)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to download voice message.")
			return fmt.Errorf("failed to download audio: %w", err)
		}

		transcribedText, err := e.audio.TranscribeAudio(ctx, audioData, "voice.ogg")
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to transcribe audio.")
			return fmt.Errorf("failed to transcribe: %w", err)
		}

		// Notify user of transcription
		_ = e.sendMessage(ctx, msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))

		// Replace the empty text with the transcribed text so the LLM processes it normally
		msg.Text = transcribedText
	} else if msg.Source == "whatsapp" && msg.MediaURL != "" {
		// Handle Twilio Voice Messages
		audioData, err := e.tw.DownloadMedia(ctx, msg.MediaURL)
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to download WhatsApp voice message.")
			return fmt.Errorf("failed to download twilio media: %w", err)
		}

		transcribedText, err := e.audio.TranscribeAudio(ctx, audioData, "voice.ogg")
		if err != nil {
			_ = e.sendMessage(ctx, msg, "❌ Failed to transcribe WhatsApp audio.")
			return fmt.Errorf("failed to transcribe twilio media: %w", err)
		}

		// Notify user of transcription
		_ = e.sendMessage(ctx, msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))

		// Replace the empty text with the transcribed text so the LLM processes it normally
		msg.Text = transcribedText
	}

	// 1. Check if there's a pending action waiting for confirmation
	pending, err := e.store.GetPendingAction(ctx, msg.UserID)
	if err != nil {
		return fmt.Errorf("failed to check pending action: %w", err)
	}

	if pending != nil {
		// Is the user confirming?
		text := strings.ToLower(strings.TrimSpace(msg.Text))
		isConfirm := text == "yes" || text == "y" || text == "confirm" || text == "do it"
		isCancel := text == "no" || text == "n" || text == "cancel" || text == "stop"

		if isCancel {
			_ = e.store.ClearPendingAction(ctx, msg.UserID)
			llmResponse = "❌ Action canceled."
			actionTaken = "cancel_pending_action"
			return e.sendMessage(ctx, msg, llmResponse)
		}

		if isConfirm {
			actionMsg := "⏳ Processing..."
			if pending.Action == "create_calendar_event" {
				actionMsg = "⏳ Creating event..."
			} else if pending.Action == "update_calendar_event" {
				actionMsg = "⏳ Updating event..."
			} else if pending.Action == "delete_calendar_event" {
				actionMsg = "⏳ Deleting event..."
			}
			_ = e.sendMessage(ctx, msg, actionMsg)
			
			actionTaken = "confirm_" + pending.Action

			// Create Calendar Client
			calClient, err := e.getCalendarClient(ctx, msg.UserID)
			if err != nil {
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				if err.Error() == "unauthorized" {
					llmResponse = "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again."
					return e.sendMessage(ctx, msg, llmResponse)
				}
				llmResponse = "⚠️ Your Google connection expired or is invalid. Please send /connect again."
				return e.sendMessage(ctx, msg, llmResponse)
			}

			// Execute Action
			if pending.Action == "create_calendar_event" {
				link, err := calClient.CreateEvent(ctx, pending.Event)
				if err != nil {
					_ = e.sendMessage(ctx, msg, "❌ Failed to create event: "+err.Error())
					return fmt.Errorf("failed to create event: %w", err)
				}
				
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				llmResponse = fmt.Sprintf("✅ **Event Created Successfully!**\n\n[View Event](%s)", link)
				return e.sendMessage(ctx, msg, llmResponse)
			} else if pending.Action == "update_calendar_event" {
				err := calClient.UpdateEvent(ctx, pending.Event.ID, pending.Event)
				if err != nil {
					llmResponse = "❌ Failed to update event: " + err.Error()
					_ = e.sendMessage(ctx, msg, llmResponse)
					return fmt.Errorf("failed to update event: %w", err)
				}
				
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				llmResponse = "✅ **Event Updated Successfully!**"
				return e.sendMessage(ctx, msg, llmResponse)
			} else if pending.Action == "delete_calendar_event" {
				err := calClient.DeleteEvent(ctx, pending.Event.ID)
				if err != nil {
					_ = e.sendMessage(ctx, msg, "❌ Failed to delete event: "+err.Error())
					return fmt.Errorf("failed to delete event: %w", err)
				}
				
				_ = e.store.ClearPendingAction(ctx, msg.UserID)
				llmResponse = "✅ **Event Deleted Successfully!**"
				return e.sendMessage(ctx, msg, llmResponse)
			}
		}

		// If it's not a clear yes/no, we can clear the action and process it as a normal message,
		// or just tell them to confirm or cancel. Let's force confirm/cancel for safety.
		llmResponse = "⚠️ You have a pending action. Please reply 'yes' to confirm, or 'cancel' to abort."
		actionTaken = "prompt_confirm"
		return e.sendMessage(ctx, msg, llmResponse)
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
		"If the user asks to change or update an event, use update_calendar_event. If they ask to cancel or delete an event, use delete_calendar_event. "+
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
		actionTaken = resp.ToolCall.Name
		replyText, _ = e.handleToolCall(ctx, msg, history, systemPrompt, resp.ToolCall)
	} else {
		// Normal text response
		actionTaken = "none"
		replyText = resp.Text
	}

	// 6. Send reply to Telegram
	if strings.TrimSpace(replyText) == "" {
		replyText = "⚠️ Sorry, I didn't get a response from the AI. Please try again."
	}
	llmResponse = replyText
	
	if err := e.sendMessage(ctx, msg, replyText); err != nil {
		return fmt.Errorf("failed to send reply message: %w", err)
	}

	// 7. Update conversation history
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "user", Content: msg.Text})
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "assistant", Content: replyText})

	return nil
}

func (e *Engine) getCalendarClient(ctx context.Context, userID string) (*calendar.Client, error) {
	tokenBytes, err := e.store.GetOAuthToken(ctx, userID)
	if err != nil || tokenBytes == nil {
		return nil, fmt.Errorf("unauthorized")
	}

	token, err := calendar.DecodeToken(tokenBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid_token")
	}

	ts := e.oauth.TokenSource(ctx, token)
	return calendar.NewClient(ctx, ts)
}

func (e *Engine) handleToolCall(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) (string, error) {
	slog.Info("llm requested tool call", "tool", toolCall.Name)
	
	if toolCall.Name == "create_calendar_event" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments into event: %w", err)
		}

		pendingAction := models.PendingAction{
			UserID:    msg.UserID,
			ChatID:    msg.ChatID,
			Action:    "create_calendar_event",
			Event:     event,
			CreatedAt: time.Now(),
		}

		if err := e.store.SetPendingAction(ctx, msg.UserID, pendingAction); err != nil {
			return "", fmt.Errorf("failed to save pending action: %w", err)
		}

		return fmt.Sprintf("🗓️ **Proposal:** I will create an event titled '%s' from %s to %s.\n\nReply **yes** to confirm or **cancel** to abort.", event.Title, event.StartTime, event.EndTime), nil
	} else if toolCall.Name == "update_calendar_event" || toolCall.Name == "delete_calendar_event" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments into event: %w", err)
		}

		pendingAction := models.PendingAction{
			UserID:    msg.UserID,
			ChatID:    msg.ChatID,
			Action:    toolCall.Name,
			Event:     event,
			CreatedAt: time.Now(),
		}

		if err := e.store.SetPendingAction(ctx, msg.UserID, pendingAction); err != nil {
			return "", fmt.Errorf("failed to save pending action: %w", err)
		}

		if toolCall.Name == "update_calendar_event" {
			return fmt.Sprintf("🗓️ **Proposal:** I will update the event '%s'.\n\nReply **yes** to confirm or **cancel** to abort.", event.Title), nil
		}
		return "🗓️ **Proposal:** I will delete the event.\n\nReply **yes** to confirm or **cancel** to abort.", nil
	} else if toolCall.Name == "list_calendar_events" {
		calClient, err := e.getCalendarClient(ctx, msg.UserID)
		if err != nil {
			if err.Error() == "unauthorized" {
				return "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.", nil
			}
			return "⚠️ Your Google connection expired or is invalid. Please send /connect again.", nil
		}

		argsJSON, _ := json.Marshal(toolCall.Args)
		var args struct {
			TimeMin string `json:"time_min"`
			TimeMax string `json:"time_max"`
			Query   string `json:"query"`
		}
		_ = json.Unmarshal(argsJSON, &args)

		events, err := calClient.ListEvents(ctx, args.TimeMin, args.TimeMax, args.Query)
		if err != nil {
			return "❌ Failed to fetch calendar events: " + err.Error(), nil
		}

		eventsJSON, _ := json.Marshal(events)
		summaryPrompt := fmt.Sprintf("Here are the calendar events I found:\n%s\nPlease fulfill the user's request using this information.", string(eventsJSON))
		
		extendedHistory := append(history, models.ChatMessage{Role: "user", Content: msg.Text})
		
		summaryResp, err := e.llm.Chat(ctx, systemPrompt, extendedHistory, summaryPrompt)
		if err != nil {
			return "❌ Failed to process events: " + err.Error(), nil
		}
		
		if summaryResp.ToolCall != nil {
			// Recursively handle the next tool call (e.g. LLM decides to delete an event it just found)
			return e.handleToolCall(ctx, msg, extendedHistory, systemPrompt, summaryResp.ToolCall)
		}
		return summaryResp.Text, nil
	}

	return "⚠️ LLM tried to call an unknown tool.", nil
}
