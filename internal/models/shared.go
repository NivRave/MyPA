package models

import (
	"strings"
	"time"
)

// Standard notification modes for shared topics.
const (
	NotifyModeInstant = "instant"
	NotifyModeDigest  = "digest"
	NotifyModeSilent  = "silent"
)

// Well-known additional topic identifiers.
const (
	TopicFamily = "family"
)

// SharedSpace defines cross-user permissions and notification preferences for a topic
// within a family group.
type SharedSpace struct {
	ID                 uint      `json:"id" gorm:"primarykey"`
	HouseholdID        string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup        string    `json:"family_group" gorm:"index;size:100;not null"`
	Topic              string    `json:"topic" gorm:"index;size:100;not null"`
	OwnerUserID        string    `json:"owner_user_id" gorm:"size:100"`
	OwnerPersonID      *string   `json:"owner_person_id,omitempty" gorm:"type:uuid;index"`       // V4-32: the person behind owner_user_id, filled by the database (migration 4)
	SharedWithUserID   string    `json:"shared_with_user_id" gorm:"size:100"`                    // Empty or "*" means all members of FamilyGroup
	SharedWithPersonID *string   `json:"shared_with_person_id,omitempty" gorm:"type:uuid;index"` // V4-32: the person behind shared_with_user_id, filled by the database (migration 4)
	CanView            bool      `json:"can_view" gorm:"default:true"`
	CanEdit            bool      `json:"can_edit" gorm:"default:true"`
	NotifyMode         string    `json:"notify_mode" gorm:"size:20;default:'instant'"` // "instant" | "digest" | "silent"
	UpdatedAt          time.Time `json:"updated_at"`
}

// SharedTaskReminder represents a shared collaborative task or reminder
// under a family shared topic with claim/complete hand-off tracking.
type SharedTaskReminder struct {
	ID                  uint       `json:"id" gorm:"primarykey"`
	HouseholdID         string     `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup         string     `json:"family_group" gorm:"index;index:idx_shared_task_reminders_family_completed_due,priority:1;size:100;not null"`
	Topic               string     `json:"topic" gorm:"index;size:100;not null"`
	Title               string     `json:"title" gorm:"type:text;not null"`
	DueTime             *time.Time `json:"due_time,omitempty" gorm:"index;index:idx_shared_task_reminders_due_completed,priority:1;index:idx_shared_task_reminders_family_completed_due,priority:3,sort:asc"`
	CreatedByUserID     string     `json:"created_by_user_id" gorm:"size:100;not null"`
	CreatedByPersonID   *string    `json:"created_by_person_id,omitempty" gorm:"type:uuid;index"` // V4-32: the person behind created_by_user_id, filled by the database (migration 4)
	AssignedToUserID    string     `json:"assigned_to_user_id,omitempty" gorm:"size:100"`
	AssignedToPersonID  *string    `json:"assigned_to_person_id,omitempty" gorm:"type:uuid;index"` // V4-32: the person behind assigned_to_user_id, filled by the database (migration 4)
	IsCompleted         bool       `json:"is_completed" gorm:"index;index:idx_shared_task_reminders_due_completed,priority:2;index:idx_shared_task_reminders_family_completed_due,priority:2;default:false"`
	CompletedByUserID   string     `json:"completed_by_user_id,omitempty" gorm:"size:100"`
	CompletedByPersonID *string    `json:"completed_by_person_id,omitempty" gorm:"type:uuid;index"` // V4-32: the person behind completed_by_user_id, filled by the database (migration 4)
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	IsSent              bool       `json:"is_sent" gorm:"index;default:false"`
	RemindedAt          *time.Time `json:"reminded_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// IsDefaultSharedTopic returns true if the topic defaults to shared within a family group
// per docs/PLAN.md (baby, house, finance, family).
func IsDefaultSharedTopic(topic string) bool {
	norm := strings.ToLower(strings.TrimSpace(topic))
	switch norm {
	case TopicBaby, TopicHouse, TopicFinance, TopicFamily:
		return true
	default:
		return false
	}
}

// DefaultNotifyModeForTopic returns the default notification mode for a topic
// when no explicit SharedSpace entry overrides it.
func DefaultNotifyModeForTopic(topic string) string {
	if IsDefaultSharedTopic(topic) {
		return NotifyModeInstant
	}
	return NotifyModeSilent
}
