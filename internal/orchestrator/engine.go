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
	"github.com/nivik/mypa/internal/markdown"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/scraper"
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
	// Format text for the specific platform
	if msg.Source == "whatsapp" {
		text = markdown.ToWhatsApp(text)
	} else if msg.Source == "telegram" {
		text = markdown.ToTelegramHTML(text)
	}

	// Chunk message to avoid hitting Twilio's 1600 character limit or Telegram limits.
	const chunkSize = 1500
	var err error

	for len(text) > 0 {
		var chunk string
		if len(text) > chunkSize {
			// Find a good breaking point
			breakIndex := chunkSize
			
			// Look for natural breaks within the last few hundred characters of the chunk
			searchSpace := text[:chunkSize]
			if idx := strings.LastIndex(searchSpace, "\n\n"); idx > chunkSize-500 {
				breakIndex = idx
			} else if idx := strings.LastIndex(searchSpace, "\n"); idx > chunkSize-300 {
				breakIndex = idx
			} else if idx := strings.LastIndex(searchSpace, ". "); idx > chunkSize-200 {
				breakIndex = idx + 1 // include the dot
			} else if idx := strings.LastIndex(searchSpace, " "); idx > chunkSize-100 {
				breakIndex = idx
			}

			chunk = strings.TrimSpace(text[:breakIndex])
			text = strings.TrimSpace(text[breakIndex:])
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
		"If the user asks to be explicitly reminded about something at a specific future time (e.g. 'Remind me at 11:00 to X'), use the schedule_reminder tool instead of creating a task or calendar event. "+
		"If the user asks about recent news, current events, or information you don't know, use the search_web tool to search the internet. "+
		"If the user tells you a personal fact or preference, use the remember_fact tool to save it for future reference. "+
		"If the user asks to check their emails, use the list_unread_emails tool. "+
		"If the user shares a URL and asks you to save it for later, use the fetch_webpage tool to get a summary, and then use the create_task tool to add it to their Google Tasks with the summary in the notes. "+
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


func (e *Engine) handleToolCall(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) (string, error) {
	slog.Info("llm requested tool call", "tool", toolCall.Name)
	
	if toolCall.Name == "create_calendar_event" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments into event: %w", err)
		}

		calClient, err := e.calendarFactory(ctx, msg.UserID)
		if err != nil {
			if err.Error() == "unauthorized" {
				return "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.", nil
			}
			return "⚠️ Your Google connection expired or is invalid. Please send /connect again.", nil
		}

		link, err := calClient.CreateEvent(ctx, event)
		if err != nil {
			return "❌ Failed to create event: " + err.Error(), nil
		}
		
		return fmt.Sprintf("✅ **Event Created Successfully!**\n\n[View Event](%s)", link), nil
	} else if toolCall.Name == "update_calendar_event" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments into event: %w", err)
		}

		calClient, err := e.calendarFactory(ctx, msg.UserID)
		if err != nil {
			if err.Error() == "unauthorized" {
				return "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.", nil
			}
			return "⚠️ Your Google connection expired or is invalid. Please send /connect again.", nil
		}

		err = calClient.UpdateEvent(ctx, event.ID, event)
		if err != nil {
			return "❌ Failed to update event: " + err.Error(), nil
		}
		
		return "✅ **Event Updated Successfully!**", nil
	} else if toolCall.Name == "delete_calendar_event" {
		argsJSON, _ := json.Marshal(toolCall.Args)
		var event models.CalendarEvent
		if err := json.Unmarshal(argsJSON, &event); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments into event: %w", err)
		}

		calClient, err := e.calendarFactory(ctx, msg.UserID)
		if err != nil {
			if err.Error() == "unauthorized" {
				return "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again.", nil
			}
			return "⚠️ Your Google connection expired or is invalid. Please send /connect again.", nil
		}

		err = calClient.DeleteEvent(ctx, event.ID)
		if err != nil {
			return "❌ Failed to delete event: " + err.Error(), nil
		}
		
		return "✅ **Event Deleted Successfully!**", nil
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
		return e.continueConversationWithToolResult(ctx, msg, history, systemPrompt, "list_calendar_events", string(eventsJSON))
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
		return e.continueConversationWithToolResult(ctx, msg, history, systemPrompt, "list_unread_emails", sb.String())
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
		return e.continueConversationWithToolResult(ctx, msg, history, systemPrompt, "read_email", body)
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
		return e.continueConversationWithToolResult(ctx, msg, history, systemPrompt, "list_tasks", sb.String())
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
		return e.continueConversationWithToolResult(ctx, msg, history, systemPrompt, "search_web", result)
	} else if toolCall.Name == "schedule_reminder" {
		message, _ := toolCall.Args["message"].(string)
		dueTimeStr, _ := toolCall.Args["due_time"].(string)

		dueTime, err := time.Parse(time.RFC3339, dueTimeStr)
		if err != nil {
			return "❌ Failed to parse due time. Please use ISO 8601 format.", nil
		}

		slog.Info("executing schedule_reminder tool", "user", msg.UserID, "message", message, "due_time", dueTime)
		
		err = e.db.SaveReminder(models.ScheduledReminder{
			UserID:    msg.UserID,
			Message:   message,
			DueTime:   dueTime,
			IsSent:    false,
			CreatedAt: time.Now(),
		})
		
		if err != nil {
			slog.Error("schedule_reminder failed", "error", err)
			return fmt.Sprintf("Error scheduling reminder: %v", err), nil
		}
		
		// Return a nice confirmation instead of recursing, as this is an action tool
		return fmt.Sprintf("✅ **Reminder Set!**\n\nI will remind you at %s: \"%s\"", dueTime.In(time.Local).Format(time.RFC822), message), nil
	} else if toolCall.Name == "fetch_webpage" {
		url, ok := toolCall.Args["url"].(string)
		if !ok {
			return "Missing url parameter", nil
		}

		slog.Info("executing fetch_webpage tool", "user", msg.UserID, "url", url)
		// Notify user that we are reading
		_ = e.sendMessage(ctx, msg, "📖 Reading webpage...")

		content, err := scraper.FetchAndExtractText(ctx, url)
		if err != nil {
			slog.Error("fetch_webpage failed", "error", err, "url", url)
			return fmt.Sprintf("Error fetching webpage: %v", err), nil
		}
		
		summaryPrompt := fmt.Sprintf("Here is the content of the webpage:\n\n%s\n\nPlease fulfill the user's original request using this information.\nIf they asked to save it for later, use the create_task tool.\nIf they asked for a summary, provide a comprehensive, detailed summary. Use explicit **bold** markdown (double asterisks) for all topics and section headers (do not use single asterisks).", content)
		
		extendedHistory := append(history, models.ChatMessage{Role: "user", Content: msg.Text})
		
		summaryResp, err := e.llm.Chat(ctx, systemPrompt, extendedHistory, summaryPrompt)
		if err != nil {
			return "❌ Failed to process webpage: " + err.Error(), nil
		}
		
		if summaryResp.ToolCall != nil {
			// Recursively handle the next tool call (e.g. LLM decides to create_task)
			return e.handleToolCall(ctx, msg, extendedHistory, systemPrompt, summaryResp.ToolCall)
		}
		return summaryResp.Text, nil
	}

	return "⚠️ LLM tried to call an unknown tool.", nil
}

func (e *Engine) continueConversationWithToolResult(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolName string, resultStr string) (string, error) {
	summaryPrompt := fmt.Sprintf("Here is the output from the %s tool:\n%s\nPlease fulfill the user's request based on this information.", toolName, resultStr)
	extendedHistory := append(history, models.ChatMessage{Role: "user", Content: msg.Text})
	
	resp, err := e.llm.Chat(ctx, systemPrompt, extendedHistory, summaryPrompt)
	if err != nil {
		return "❌ Failed to process tool output: " + err.Error(), nil
	}
	
	if resp.ToolCall != nil {
		// Mock a new message so the next recursion uses the summaryPrompt as the "user" context
		nextMsg := msg
		nextMsg.Text = summaryPrompt
		return e.handleToolCall(ctx, nextMsg, extendedHistory, systemPrompt, resp.ToolCall)
	}
	return resp.Text, nil
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

// CheckAndSendReminders checks the database for due reminders and sends them.
func (e *Engine) CheckAndSendReminders() {
	reminders, err := e.db.GetDueReminders()
	if err != nil {
		slog.Error("failed to get due reminders", "error", err)
		return
	}

	for _, reminder := range reminders {
		log, err := e.db.GetLastAuditLogForUser(reminder.UserID)
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
		_ = e.store.AppendChatHistory(context.Background(), reminder.UserID, models.ChatMessage{Role: "assistant", Content: formattedMsg})
	}
}
