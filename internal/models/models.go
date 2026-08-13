package models

import "time"

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
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	StartTime   string `json:"start_time"`   // ISO 8601
	EndTime     string `json:"end_time"`     // ISO 8601
	Description string `json:"description,omitempty"`
	Timezone    string `json:"timezone"`
}

// PendingAction represents an action awaiting user confirmation.
type PendingAction struct {
	// UserID is the user who initiated the action.
	UserID string `json:"user_id"`

	// ChatID is the chat where the confirmation should be sent.
	ChatID string `json:"chat_id"`

	// Action is the type of action (e.g., "create_calendar_event").
	Action string `json:"action"`

	// Event holds the parsed calendar event details.
	Event CalendarEvent `json:"event"`

	// CreatedAt is when the pending action was created.
	CreatedAt time.Time `json:"created_at"`
}

// ChatMessage represents a single message in conversation history.
type ChatMessage struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// AuditLog represents a single interaction logged to the database.
type AuditLog struct {
	ID          uint      `json:"id" gorm:"primarykey"`
	UserID      string    `json:"user_id" gorm:"index"`
	ChatID      string    `json:"chat_id"`
	Source      string    `json:"source"`
	UserMessage string    `json:"user_message"`
	LLMResponse string    `json:"llm_response"`
	ActionTaken string    `json:"action_taken"`
	CreatedAt   time.Time `json:"created_at"`
}
