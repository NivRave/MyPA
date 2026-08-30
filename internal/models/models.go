package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// Message represents a normalized incoming message from any channel.
type Message struct {
	// ID is the unique message identifier from the source platform.
	ID string `json:"id"`

	// UserID is the platform-specific user identifier.
	UserID string `json:"user_id"`

	// ChatID is the platform-specific chat/conversation identifier.
	ChatID string `json:"chat_id"`

	// Text is the message content.
	Text string `json:"text"`

	// VoiceFileID holds the Telegram file ID if the message is a voice note.
	VoiceFileID string `json:"voice_file_id,omitempty"`
	// PhotoFileID holds the Telegram file ID if the message contains a photo.
	PhotoFileID string `json:"photo_file_id,omitempty"`
	// AudioFileID is a generic field for audio identifiers.
	AudioFileID string `json:"audio_file_id,omitempty"`
	// MediaURL is used for external media links (e.g., WhatsApp voice notes).
	MediaURL string `json:"media_url,omitempty"`

	// Source identifies the originating platform (e.g., "telegram").
	Source string `json:"source"`

	// Timestamp is when the message was sent.
	Timestamp time.Time `json:"timestamp"`
}

// CalendarEvent represents a calendar event to be created.
type CalendarEvent struct {
	ID          string   `json:"id,omitempty"`
	Title       string   `json:"title"`
	StartTime   string   `json:"start_time"`   // ISO 8601
	EndTime     string   `json:"end_time"`     // ISO 8601
	Description string   `json:"description,omitempty"`
	Timezone    string   `json:"timezone"`
	Recurrence  []string `json:"recurrence,omitempty"`
}

// Contact represents a Google Contact.
type Contact struct {
	ResourceName string `json:"resource_name"`
	Name         string `json:"name,omitempty"`
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
}
// ChatMessage represents a single message in conversation history.
type ChatMessage struct {
	Role          string            `json:"role"` // "user", "assistant", or "function"
	Content       string            `json:"content,omitempty"`
	ToolCall      *FunctionCall     `json:"tool_call,omitempty"`
	ToolResponse  *FunctionResponse `json:"tool_response,omitempty"`
}

type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type FunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// AuditSession represents a complete user interaction session.
type AuditSession struct {
	ID              string    `json:"id" gorm:"primarykey;type:uuid"`
	UserID          string    `json:"user_id" gorm:"index"`
	ChatID          string    `json:"chat_id"`
	Source          string    `json:"source"`
	UserPrompt      string    `json:"user_prompt"`
	FinalResponse   string    `json:"final_response"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	TotalDurationMs int64     `json:"total_duration_ms"`
	TotalTokens     int       `json:"total_tokens"`
	Status          string    `json:"status"` // "success" or "failed"
}

// AuditEvent represents a single action/span within a session.
type AuditEvent struct {
	ID               string    `json:"id" gorm:"primarykey;type:uuid"`
	SessionID        string    `json:"session_id" gorm:"index"`
	EventType        string    `json:"event_type"` // e.g., "llm_inference", "tool_execution"
	ActionName       string    `json:"action_name"`
	RequestPayload   string    `json:"request_payload" gorm:"type:jsonb"`
	ResponsePayload  string    `json:"response_payload" gorm:"type:jsonb"`
	ErrorMessage     string    `json:"error_message"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	DurationMs       int64     `json:"duration_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
}

// User represents an allowed user configured via .env
type User struct {
	PlatformID  string `json:"platform_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`         // "admin" or "family"
	FamilyGroup string `json:"family_group"` // e.g., "MyFamily"
}

// Memory represents a long-term fact or preference about a user.
type Memory struct {
	ID        uint            `json:"id" gorm:"primarykey"`
	UserID    string          `json:"user_id" gorm:"index"`
	Scope     string          `json:"scope" gorm:"index;default:'personal'"` // "personal" or "family"
	Fact      string          `json:"fact"`
	Embedding pgvector.Vector `json:"embedding" gorm:"type:vector(3072)"`
	CreatedAt time.Time       `json:"created_at"`
}

// ScheduledReminder represents a message to be sent to a user at a specific time.
type ScheduledReminder struct {
	ID        uint      `json:"id" gorm:"primarykey"`
	UserID    string    `json:"user_id" gorm:"index"`
	Message   string    `json:"message"`
	DueTime   time.Time `json:"due_time" gorm:"index"`
	IsSent    bool      `json:"is_sent" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}
