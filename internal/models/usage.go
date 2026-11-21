package models

import "time"

// ModelUsage is the tokens of one chat model call (V4-04). The calls of one turn share TurnID,
// the ID of the turn's audit session, so a turn's cost is the sum of its rows. PersonID is the
// person the turn was for; it is NULL for a call outside a person's turn, and becomes NULL when
// the person is removed, so the install's daily total keeps the call.
type ModelUsage struct {
	ID       uint64  `json:"id" gorm:"primaryKey"`
	PersonID *string `json:"person_id,omitempty" gorm:"type:uuid;index:idx_model_usage_person_created,priority:1"`
	TurnID   string  `json:"turn_id" gorm:"not null;default:'';index"`
	// Provider and Model name who answered (after a failover, not the primary).
	Provider     string    `json:"provider" gorm:"not null;default:''"`
	Model        string    `json:"model" gorm:"not null;default:''"`
	InputTokens  int64     `json:"input_tokens" gorm:"not null;default:0"`
	OutputTokens int64     `json:"output_tokens" gorm:"not null;default:0"`
	CreatedAt    time.Time `json:"created_at" gorm:"not null;index;index:idx_model_usage_person_created,priority:2"`
}

// TableName pins the table name.
func (ModelUsage) TableName() string { return "model_usage" }

// UsageDay is one person's tokens on one calendar day (YYYY-MM-DD on the install's clock).
type UsageDay struct {
	PersonID     string
	Day          string
	InputTokens  int64
	OutputTokens int64
	// Calls is the number of model calls; Turns the number of turns they belonged to.
	Calls int
	Turns int
}

// TurnUsage is the tokens of one turn: the sum of its model calls.
type TurnUsage struct {
	TurnID       string
	PersonID     string
	StartedAt    time.Time
	InputTokens  int64
	OutputTokens int64
	Calls        int
	// Providers lists who answered the turn's calls, sorted, comma-separated.
	Providers string
}
