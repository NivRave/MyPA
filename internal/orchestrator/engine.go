package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/oauth2"

	"google.golang.org/genai"
)

// Engine is the core orchestrator that connects all components.
type Engine struct {
	consumer    *broker.Consumer
	store       *state.Store
	db          DBClient
	llm         LLMClient
	tgClient    TelegramClient
	twClient    TwilioClient
	audioClient AudioClient
	oauthCfg     *calendar.OAuthConfig
	gmailClient  GmailClient
	tasksClient  TasksClient
	tavilyClient TavilyClient
	calendarFactory func(context.Context, string) (CalendarClient, error)
	timezone     string
	wg           sync.WaitGroup
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
	tavilyC TavilyClient,
	audio AudioClient,
	tz string,
) *Engine {
	e := &Engine{
		consumer:    cons,
		store:       store,
		db:          db,
		llm:         llm,
		tgClient:    tg,
		twClient:    tw,
		oauthCfg:     oauth,
		gmailClient:  gmailC,
		tasksClient:  tasksC,
		tavilyClient: tavilyC,
		audioClient:  audio,
		timezone:     tz,
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

// Wait blocks until all background tasks finish.
func (e *Engine) Wait() {
	e.wg.Wait()
}

// sendMessage sends a text message to the user, breaking it into smaller chunks if necessary to avoid platform limits.
func (e *Engine) sendMessage(ctx context.Context, msg models.Message, text string) error {
	// Chunk message to avoid hitting Twilio's 1600 character limit or Telegram limits.
	const chunkSize = 1500
	var err error

	for len(text) > 0 {
		var chunk string
		if len(text) > chunkSize {
			// Try to find a good breaking point (newline or space)
			breakIndex := chunkSize
			for i := chunkSize; i > chunkSize-100; i-- {
				if text[i] == '\n' || text[i] == ' ' {
					breakIndex = i
					break
				}
			}
			chunk = text[:breakIndex]
			text = text[breakIndex:]
		} else {
			chunk = text
			text = ""
		}

		if msg.Source == "whatsapp" {
			err = e.twClient.SendMessage(ctx, msg.UserID, chunk)
		} else {
			err = e.tgClient.SendMessage(ctx, msg.ChatID, chunk)
		}

		if err != nil {
			return err
		}
	}
	return nil
}

// processMessage is the main entry point for handling an incoming user message.
// It orchestrates transcription, command handling, confirmation flows, and LLM reasoning.
func (e *Engine) processMessage(ctx context.Context, msg models.Message) error {
	slog.Info("processing message", "user_id", msg.UserID, "text", msg.Text)

	var actionTaken string
	var llmResponse string

	defer func() {
		e.wg.Add(1)
		// Log the interaction asynchronously
		go func(msgText string) {
			defer e.wg.Done()
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
				slog.Error("failed to save audit log", "error", fmt.Errorf("LogInteraction error: %w", err))
			}
		}(msg.Text) // Pass msg.Text in case it gets mutated (like voice transcription)
	}()

	// Check for commands
	if msg.Text == "/connect" {
		url := e.oauthCfg.AuthCodeURL(msg.UserID)
		llmResponse = fmt.Sprintf("🔗 [Click here to connect your Google Calendar](%s)", url)
		actionTaken = "connect_command"
		return e.sendMessage(ctx, msg, llmResponse)
	}

	// 0. Intercept and transcribe Voice Messages
	if msg.VoiceFileID != "" {
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

		// Replace the empty text with the transcribed text so the LLM processes it normally
		msg.Text = transcribedText
	} else if msg.Source == "whatsapp" && msg.MediaURL != "" {
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
			calClient, err := e.calendarFactory(ctx, msg.UserID)
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
		"If the user asks to manage tasks, to-dos, or lists, use the Google Tasks tools (list_tasks, create_task, complete_task, delete_task). "+
		"If the user asks about recent news, current events, or information you don't know, use the search_web tool to search the internet. "+
		"Do not ask for confirmation if they provided enough details (title, start, end). "+
		"If the user tells you a personal fact or preference, use the remember_fact tool to save it for future reference. "+
		"If the user asks to check their emails, use the list_unread_emails tool. "+
		"IMPORTANT: If a tool returns JSON or raw data (like list_unread_emails or list_tasks), you MUST summarize and format it into a clean, friendly, conversational response (e.g. using bullet points). NEVER output raw JSON to the user. "+
		"IMPORTANT: The user is in Israel. The week starts on Sunday and ends on Thursday (Friday and Saturday are the weekend). When reasoning about 'next week' or 'this week', start the week on Sunday.",
		time.Now().Format(time.RFC1123), e.timezone,
	)

	// Search long-term semantic memory
	embedding, err := e.llm.GenerateEmbedding(ctx, msg.Text)
	if err == nil && len(embedding) > 0 {
		memories, _ := e.db.SearchMemories(msg.UserID, pgvector.NewVector(embedding), 5)
		if len(memories) > 0 {
			var facts []string
			for _, m := range memories {
				facts = append(facts, "- "+m.Fact)
			}
			systemPrompt += "\n\nRelevant facts about the user from past conversations:\n" + strings.Join(facts, "\n")
		}
	}

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

	ts := e.oauthCfg.TokenSource(ctx, token)
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
		calClient, err := e.calendarFactory(ctx, msg.UserID)
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
	} else if toolCall.Name == "remember_fact" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var args struct {
			Fact string `json:"fact"`
		}
		_ = json.Unmarshal(argsJSON, &args)

		embedding, err := e.llm.GenerateEmbedding(ctx, args.Fact)
		if err != nil {
			slog.Error("failed to generate embedding for fact", "error", err)
			return "❌ Failed to save memory (embedding error).", nil
		}

		err = e.db.SaveMemory(models.Memory{
			UserID:    msg.UserID,
			Fact:      args.Fact,
			Embedding: pgvector.NewVector(embedding),
			CreatedAt: time.Now(),
		})
		if err != nil {
			slog.Error("failed to save memory to db", "error", err)
			return "❌ Failed to save memory to database.", nil
		}
		return "✅ Got it! I've saved that to my long-term memory.", nil
	} else if toolCall.Name == "list_unread_emails" {
		maxResults := int64(5)
		if val, ok := toolCall.Args["max_results"]; ok {
			maxResults = int64(val.(float64))
		}

		slog.Info("executing list_unread_emails tool", "user", msg.UserID, "max_results", maxResults)
		emails, err := e.gmailClient.ListUnreadEmails(ctx, msg.UserID, maxResults)
		if err != nil {
			slog.Error("list_unread_emails failed", "error", err)
			return fmt.Sprintf("Error checking emails: %v", err), nil
		}
		if len(emails) == 0 {
			return "You have no unread emails.", nil
		}
		
		var sb strings.Builder
		sb.WriteString("📬 *Unread Emails:*\n\n")
		for i, email := range emails {
			sb.WriteString(fmt.Sprintf("%d. *From:* %s\n*Subject:* %s\n*Snippet:* %s\n\n", i+1, email.From, email.Subject, email.Snippet))
		}
		return sb.String(), nil
	} else if toolCall.Name == "read_email" {
		messageID, ok := toolCall.Args["message_id"].(string)
		if !ok {
			return "Missing message_id parameter", nil
		}

		slog.Info("executing read_email tool", "user", msg.UserID, "message_id", messageID)
		body, err := e.gmailClient.ReadEmail(ctx, msg.UserID, messageID)
		if err != nil {
			slog.Error("read_email failed", "error", err)
			return fmt.Sprintf("Error reading email: %v", err), nil
		}
		return body, nil
	} else if toolCall.Name == "draft_email_reply" {
		messageID, okID := toolCall.Args["message_id"].(string)
		replyText, okText := toolCall.Args["reply_text"].(string)
		
		if !okID || !okText {
			return "Missing message_id or reply_text parameters", nil
		}

		slog.Info("executing draft_email_reply tool", "user", msg.UserID, "message_id", messageID)
		err := e.gmailClient.DraftReply(ctx, msg.UserID, messageID, replyText)
		if err != nil {
			slog.Error("draft_email_reply failed", "error", err)
			return fmt.Sprintf("Error drafting reply: %v", err), nil
		}
		return "Draft created successfully! The user can review it in their Gmail app.", nil
	} else if toolCall.Name == "list_tasks" {
		slog.Info("executing list_tasks tool", "user", msg.UserID)
		tasks, err := e.tasksClient.ListTasks(ctx, msg.UserID)
		if err != nil {
			slog.Error("list_tasks failed", "error", err)
			return fmt.Sprintf("Error listing tasks: %v", err), nil
		}
		if len(tasks) == 0 {
			return "You have no tasks on your default list.", nil
		}
		
		var sb strings.Builder
		sb.WriteString("📝 *Your Tasks:*\n\n")
		for _, t := range tasks {
			dueStr := ""
			if t.Due != "" {
				// Parse and format due date if possible
				if parsed, err := time.Parse(time.RFC3339, t.Due); err == nil {
					dueStr = fmt.Sprintf(" (Due: %s)", parsed.Format("Jan 02"))
				}
			}
			notesStr := ""
			if t.Notes != "" {
				notesStr = fmt.Sprintf("\n   _Notes: %s_", t.Notes)
			}
			sb.WriteString(fmt.Sprintf("- *%s*%s%s\n", t.Title, dueStr, notesStr))
		}
		return sb.String(), nil
	} else if toolCall.Name == "create_task" {
		title, _ := toolCall.Args["title"].(string)
		notes, _ := toolCall.Args["notes"].(string)
		due, _ := toolCall.Args["due"].(string)

		slog.Info("executing create_task tool", "user", msg.UserID, "title", title)
		err := e.tasksClient.CreateTask(ctx, msg.UserID, title, notes, due)
		if err != nil {
			slog.Error("create_task failed", "error", err)
			return fmt.Sprintf("Error creating task: %v", err), nil
		}
		return "Task created successfully.", nil
	} else if toolCall.Name == "complete_task" {
		taskID, ok := toolCall.Args["task_id"].(string)
		if !ok {
			return "Missing task_id parameter", nil
		}

		slog.Info("executing complete_task tool", "user", msg.UserID, "task_id", taskID)
		err := e.tasksClient.CompleteTask(ctx, msg.UserID, taskID)
		if err != nil {
			slog.Error("complete_task failed", "error", err)
			return fmt.Sprintf("Error completing task: %v", err), nil
		}
		return "Task marked as completed.", nil
	} else if toolCall.Name == "delete_task" {
		taskID, ok := toolCall.Args["task_id"].(string)
		if !ok {
			return "Missing task_id parameter", nil
		}

		slog.Info("executing delete_task tool", "user", msg.UserID, "task_id", taskID)
		err := e.tasksClient.DeleteTask(ctx, msg.UserID, taskID)
		if err != nil {
			slog.Error("delete_task failed", "error", err)
			return fmt.Sprintf("Error deleting task: %v", err), nil
		}
		return "Task deleted successfully.", nil
	} else if toolCall.Name == "search_web" {
		query, ok := toolCall.Args["query"].(string)
		if !ok {
			return "Missing query parameter", nil
		}

		slog.Info("executing search_web tool", "user", msg.UserID, "query", query)
		// Notify user that we are searching
		_ = e.sendMessage(ctx, msg, "🔍 Searching the web...")

		result, err := e.tavilyClient.Search(ctx, query)
		if err != nil {
			slog.Error("search_web failed", "error", err)
			return fmt.Sprintf("Error searching the web: %v", err), nil
		}
		return result, nil
	}

	return "⚠️ LLM tried to call an unknown tool.", nil
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
		// We'll just look up the most recent audit log to find the user's preferred platform/chat ID.
		log, err := e.db.GetLastAuditLogForUser(userID)
		if err != nil || log == nil {
			slog.Warn("failed to get last audit log for user", "user_id", userID, "error", err)
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
			if err := e.processMessage(bgCtx, m); err != nil {
				slog.Error("failed to process proactive message", "user", m.UserID, "error", err)
			}
		}(msg)
	}
}
