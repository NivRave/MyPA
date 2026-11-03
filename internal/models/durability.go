package models

import (
	"time"
)

// EncryptedCredential stores AES-256-GCM encrypted OAuth tokens or session metadata
// as a durable fallback in PostgreSQL when Redis is flushed or unavailable.
type EncryptedCredential struct {
	UserID     string    `json:"user_id" gorm:"primaryKey"`
	Provider   string    `json:"provider" gorm:"primaryKey"` // e.g. "google_oauth" | "session"
	Ciphertext []byte    `json:"ciphertext" gorm:"type:bytea"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MutationLog records every state-changing action performed by the agent or user,
// enabling full auditability, rollback ("undo last action"), and partner notification sync.
type MutationLog struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	UserID      string    `json:"user_id" gorm:"index"`
	FamilyGroup string    `json:"family_group" gorm:"index"`
	Topic       string    `json:"topic" gorm:"index"`       // e.g. "baby", "house", "finance", "schedule", "memory"
	Action      string    `json:"action"`                  // e.g. "create", "update", "delete", "complete", "undo"
	EntityType  string    `json:"entity_type" gorm:"index"` // e.g. "BabyCareLog", "ScheduleEvent"
	EntityID    string    `json:"entity_id" gorm:"index"`
	BeforeJSON  string    `json:"before_json" gorm:"type:jsonb"`
	AfterJSON   string    `json:"after_json" gorm:"type:jsonb"`
	Summary     string    `json:"summary"`
	IsUndone    bool      `json:"is_undone" gorm:"default:false;index"`
	CreatedAt   time.Time `json:"created_at" gorm:"index"`
}

// IdempotencyRecord stores execution tokens to guard against duplicate tool execution
// or duplicate webhooks.
type IdempotencyRecord struct {
	Key             string    `json:"key" gorm:"primaryKey"`
	UserID          string    `json:"user_id" gorm:"index"`
	ToolName        string    `json:"tool_name"`
	ResponseSummary string    `json:"response_summary"`
	ExpiresAt       time.Time `json:"expires_at" gorm:"index"`
}
