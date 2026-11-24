package models

import (
	"time"
)

// ExternalAPIConnector stores dynamic on-demand REST/Webhook configurations
// with AES-256-GCM encrypted authorization headers.
type ExternalAPIConnector struct {
	ID                   uint      `json:"id" gorm:"primaryKey"`
	UserID               string    `json:"user_id" gorm:"index"`
	PersonID             *string   `json:"person_id,omitempty" gorm:"type:uuid;index"`   // V4-32: the person behind user_id, filled by the database (migration 4)
	HouseholdID          string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup          string    `json:"family_group" gorm:"index"`
	Name                 string    `json:"name" gorm:"index"`
	BaseURL              string    `json:"base_url"`
	DefaultMethod        string    `json:"default_method"`
	EncryptedAuthHeaders []byte    `json:"encrypted_auth_headers" gorm:"type:bytea"`
	Description          string    `json:"description"`
	CreatedAt            time.Time `json:"created_at"`
}

// AutomationRule represents an event-driven or conditional trigger rule (When -> If -> Then)
// that executes webhooks, instructions, or notifications upon matching MutationEvents.
type AutomationRule struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	UserID            string    `json:"user_id" gorm:"index"`
	PersonID          *string   `json:"person_id,omitempty" gorm:"type:uuid;index"`   // V4-32: the person behind user_id, filled by the database (migration 4)
	HouseholdID       string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup       string    `json:"family_group" gorm:"index"`
	Name              string    `json:"name"`
	TriggerType       string    `json:"trigger_type"` // "event_mutation", "cron", "keyword", "threshold"
	TriggerTopic      string    `json:"trigger_topic" gorm:"index"`
	ConditionJSON     string    `json:"condition_json" gorm:"type:jsonb"`
	ActionInstruction string    `json:"action_instruction"`
	IsActive          bool      `json:"is_active" gorm:"index"` // no tag default: GORM would store it in place of a false (BL-62)
	CreatedAt         time.Time `json:"created_at"`
}

// UserPreference stores per-user runtime customization, persona settings,
// quiet hours, and standing instructions dynamically injected into LLM system prompts.
type UserPreference struct {
	UserID               string    `json:"user_id" gorm:"primaryKey"`
	PersonID             *string   `json:"person_id,omitempty" gorm:"type:uuid;index"` // V4-32: the person behind user_id, filled by the database (migration 4)
	PreferredLanguage    string    `json:"preferred_language"`
	TonePersona          string    `json:"tone_persona"`
	DefaultCurrency      string    `json:"default_currency"`
	QuietHoursStart      string    `json:"quiet_hours_start"`
	QuietHoursEnd        string    `json:"quiet_hours_end"`
	BriefingTopicsJSON   string    `json:"briefing_topics_json" gorm:"type:jsonb"`
	StandingInstructions string    `json:"standing_instructions"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// PersonalCRMContact stores family, friends, and service contacts with
// important dates (birthdays, anniversaries), gift ideas, sizes, and medical notes.
type PersonalCRMContact struct {
	ID                 uint      `json:"id" gorm:"primaryKey"`
	UserID             string    `json:"user_id" gorm:"index"`
	PersonID           *string   `json:"person_id,omitempty" gorm:"type:uuid;index"`   // V4-32: the person behind user_id, filled by the database (migration 4)
	HouseholdID        string    `json:"household_id" gorm:"type:uuid;not null;index"` // V4-30: the tenant key; FamilyGroup is still written until V4-44
	FamilyGroup        string    `json:"family_group" gorm:"index"`
	PersonName         string    `json:"person_name" gorm:"index"`
	Relationship       string    `json:"relationship"`
	ImportantDatesJSON string    `json:"important_dates_json" gorm:"type:jsonb"`
	GiftIdeasAndSizes  string    `json:"gift_ideas_and_sizes"`
	MedicalNotes       string    `json:"medical_notes"`
	UpdatedAt          time.Time `json:"updated_at"`
}
