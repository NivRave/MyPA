package models

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// The inbound message type is channel.Inbound (V4-31); it replaced models.Message.

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
	Role          string             `json:"role"` // "user", "assistant", or "function"
	Content       string             `json:"content,omitempty"`
	ToolCalls     []FunctionCall     `json:"tool_calls,omitempty"`
	ToolResponses []FunctionResponse `json:"tool_responses,omitempty"`
	PhotoData     []byte             `json:"photo_data,omitempty"`
	PhotoMimeType string             `json:"photo_mime_type,omitempty"`
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
	UserID          string    `json:"user_id" gorm:"index;index:idx_audit_sessions_user_start,priority:1"`
	ChatID          string    `json:"chat_id"`
	Source          string    `json:"source"`
	UserPrompt      string    `json:"user_prompt"`
	FinalResponse   string    `json:"final_response"`
	StartTime       time.Time `json:"start_time" gorm:"index:idx_audit_sessions_start_time;index:idx_audit_sessions_user_start,priority:2,sort:desc"`
	EndTime         time.Time `json:"end_time"`
	TotalDurationMs int64     `json:"total_duration_ms"`
	TotalTokens     int       `json:"total_tokens"`
	Status          string    `json:"status"` // "success" or "failed"
	// ToolSummary is a bounded one-line record of the tools the turn executed and their results
	// (P2-9). It travels with the session into the 30-day cold archive.
	ToolSummary string `json:"tool_summary,omitempty" gorm:"type:text"`
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

// User represents an allowed user in the system.
type User struct {
	ID          string    `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	PlatformID  string    `json:"platform_id" gorm:"uniqueIndex"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`         // "admin" or "family"
	FamilyGroup string    `json:"family_group"` // e.g., "MyFamily"
	CreatedAt   time.Time `json:"created_at"`
}

// PairingCode represents a temporary code used to onboard new users.
type PairingCode struct {
	ID          string    `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	Code        string    `json:"code" gorm:"uniqueIndex"`
	Role        string    `json:"role"`
	FamilyGroup string    `json:"family_group"`
	ExpiresAt   time.Time `json:"expires_at"`
	IsUsed      bool      `json:"is_used" gorm:"default:false"`
	CreatedAt   time.Time `json:"created_at"`
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

// Workflow represents a scheduled recurring task (macro).
type Workflow struct {
	ID             uint      `json:"id" gorm:"primarykey"`
	UserID         string    `json:"user_id" gorm:"index"`
	Name           string    `json:"name"`
	CronExpression string    `json:"cron_expression"`
	Instruction    string    `json:"instruction"`
	IsActive       bool      `json:"is_active" gorm:"index"`
	CreatedAt      time.Time `json:"created_at"`
}
