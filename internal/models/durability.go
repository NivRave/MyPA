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
	UserID      string    `json:"user_id" gorm:"index;index:idx_mutation_logs_user_created,priority:1"`
	FamilyGroup string    `json:"family_group" gorm:"index;index:idx_mutation_logs_family_created,priority:1;index:idx_mutation_logs_family_topic_created,priority:1"`
	Topic       string    `json:"topic" gorm:"index;index:idx_mutation_logs_family_topic_created,priority:2"`       // e.g. "baby", "house", "finance", "schedule", "memory"
	Action      string    `json:"action"`                  // e.g. "create", "update", "delete", "complete", "undo"
	EntityType  string    `json:"entity_type" gorm:"index"` // e.g. "BabyCareLog", "ScheduleEvent"
	EntityID    string    `json:"entity_id" gorm:"index"`
	BeforeJSON  string    `json:"before_json" gorm:"type:jsonb"`
	AfterJSON   string    `json:"after_json" gorm:"type:jsonb"`
	Summary     string    `json:"summary"`
	IsUndone    bool      `json:"is_undone" gorm:"default:false;index"`
	CreatedAt   time.Time `json:"created_at" gorm:"index;index:idx_mutation_logs_family_created,priority:2,sort:desc;index:idx_mutation_logs_user_created,priority:2,sort:desc;index:idx_mutation_logs_family_topic_created,priority:3,sort:desc"`
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

// RollbackResult provides unified, structured feedback on completed rollback operations.
type RollbackResult struct {
	MutationID    string `json:"mutation_id"`
	Topic         string `json:"topic"`
	Action        string `json:"action"`
	EntityType    string `json:"entity_type"`
	EntityID      string `json:"entity_id"`
	Summary       string `json:"summary"`
	Detail        string `json:"detail"`
	AlreadyUndone bool   `json:"already_undone"`
}

