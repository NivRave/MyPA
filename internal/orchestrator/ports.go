package orchestrator

import (
	"context"

	"github.com/nivik/mypa/internal/gmail"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"google.golang.org/api/calendar/v3"
	tasksapi "google.golang.org/api/tasks/v1"
)

// TelegramClient defines the interface for interacting with Telegram.
type TelegramClient interface {
	SendMessage(ctx context.Context, chatID, text string) error
	GetFile(ctx context.Context, fileID string) (string, error)
	DownloadFile(ctx context.Context, filePath string) ([]byte, error)
}

// TwilioClient defines the interface for interacting with Twilio/WhatsApp.
type TwilioClient interface {
	SendMessage(ctx context.Context, to, body string) error
	DownloadMedia(ctx context.Context, mediaURL string) ([]byte, error)
}

// AudioClient defines the interface for transcribing audio.
type AudioClient interface {
	TranscribeAudio(ctx context.Context, audioData []byte, filename string) (string, error)
}

// LLMClient defines the interface for interacting with the LLM.
type LLMClient interface {
	Chat(ctx context.Context, systemPrompt string, history []models.ChatMessage, userMessage string) (*llm.Response, error)
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

// CalendarClient defines the interface for interacting with Google Calendar.
type CalendarClient interface {
	CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error)
	ListEvents(ctx context.Context, timeMin, timeMax string, q string) ([]*calendar.Event, error)
	UpdateEvent(ctx context.Context, eventID string, ev models.CalendarEvent) error
	DeleteEvent(ctx context.Context, eventID string) error
}

// GmailClient defines the interface for interacting with Gmail.
type GmailClient interface {
	ListUnreadEmails(ctx context.Context, userID string, maxResults int64) ([]gmail.UnreadEmail, error)
	ReadEmail(ctx context.Context, userID string, messageID string) (string, error)
	DraftReply(ctx context.Context, userID string, messageID string, replyText string) error
}

// TasksClient defines the interface for Google Tasks.
type TasksClient interface {
	ListTasks(ctx context.Context, userID string) ([]*tasksapi.Task, error)
	CreateTask(ctx context.Context, userID string, title, notes, due string) error
	CompleteTask(ctx context.Context, userID string, taskID string) error
	DeleteTask(ctx context.Context, userID string, taskID string) error
}

// DBClient defines the interface for database operations.
type DBClient interface {
	LogInteraction(log models.AuditLog) error
	SaveMemory(memory models.Memory) error
	SearchMemories(userID string, embedding pgvector.Vector, limit int) ([]models.Memory, error)
	GetUniqueUsers() ([]string, error)
	GetLastAuditLogForUser(userID string) (*models.AuditLog, error)
	SaveReminder(reminder models.ScheduledReminder) error
	GetDueReminders() ([]models.ScheduledReminder, error)
	MarkReminderSent(id uint) error
}

// TavilyClient defines the interface for web search.
type TavilyClient interface {
	Search(ctx context.Context, query string) (string, error)
}
