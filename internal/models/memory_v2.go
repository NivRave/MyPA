package models

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

// Supported canonical memory topics.
const (
	TopicBaby     = "baby"
	TopicHouse    = "house"
	TopicFinance  = "finance"
	TopicHealth   = "health"
	TopicWork     = "work"
	TopicPersonal = "personal"
)

// Supported canonical entity profile types.
const (
	EntityTypeBaby     = "baby"
	EntityTypeProperty = "property"
	EntityTypeVehicle  = "vehicle"
	EntityTypePerson   = "person"
)

// TopicMemory represents a structured, topic-isolated, temporal long-term memory fact.
// Unlike flat vector memories, TopicMemory supports hierarchical categorization (topic + subtopic),
// structured tag arrays in JSONB, temporal validity windows, automatic superseding, and pinning.
type TopicMemory struct {
	ID              uint       `json:"id" gorm:"primarykey"`
	UserID          string     `json:"user_id" gorm:"index"`
	FamilyGroup     string     `json:"family_group" gorm:"index"`
	Topic           string     `json:"topic" gorm:"index"` // "baby" | "house" | "finance" | "health" | "work" | "personal"
	Subtopic        string     `json:"subtopic" gorm:"index"`
	TagsJSON        string     `json:"tags_json" gorm:"type:jsonb"`
	Fact            string     `json:"fact"`
	Scope           string     `json:"scope" gorm:"index;default:'personal'"` // "personal" | "family"
	CreatedByUserID string     `json:"created_by_user_id"`
	SupersededByID  *uint      `json:"superseded_by_id,omitempty" gorm:"index"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until,omitempty"`
	IsPinned        bool            `json:"is_pinned" gorm:"default:false"`
	Embedding       pgvector.Vector `json:"embedding,omitempty" gorm:"type:vector(3072)"`
	CreatedAt       time.Time       `json:"created_at"`
}

// TableName explicitly specifies the database table name for GORM.
func (TopicMemory) TableName() string {
	return "topic_memories"
}

// BeforeCreate ensures that uninitialized embeddings are set to a 3072-dimensional zero vector
// to satisfy PostgreSQL pgvector constraints (preventing SQLSTATE 22000: vector must have at least 1 dimension).
func (m *TopicMemory) BeforeCreate(tx *gorm.DB) error {
	if len(m.Embedding.Slice()) == 0 {
		m.Embedding = pgvector.NewVector(make([]float32, 3072))
	}
	return nil
}

// BeforeSave ensures that uninitialized embeddings are set to a 3072-dimensional zero vector
// on save/update operations.
func (m *TopicMemory) BeforeSave(tx *gorm.DB) error {
	if len(m.Embedding.Slice()) == 0 {
		m.Embedding = pgvector.NewVector(make([]float32, 3072))
	}
	return nil
}

// Tags deserializes the TagsJSON field into a slice of string tags.
func (m *TopicMemory) Tags() []string {
	if strings.TrimSpace(m.TagsJSON) == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(m.TagsJSON), &tags); err != nil {
		return nil
	}
	return tags
}

// SetTags serializes a slice of tags into the TagsJSON field.
func (m *TopicMemory) SetTags(tags []string) {
	if len(tags) == 0 {
		m.TagsJSON = "[]"
		return
	}
	b, err := json.Marshal(tags)
	if err != nil {
		m.TagsJSON = "[]"
		return
	}
	m.TagsJSON = string(b)
}

// IsActive returns true if the memory has not been superseded and is currently valid.
func (m *TopicMemory) IsActive() bool {
	if m.SupersededByID != nil {
		return false
	}
	if m.ValidUntil != nil && m.ValidUntil.Before(time.Now()) {
		return false
	}
	return true
}

// EntityProfile represents a living, structured entity card (e.g. BabyProfile, Property, Vehicle)
// whose attributes are maintained in JSONB and deterministically updated to prevent contradictory vector rows.
type EntityProfile struct {
	ID              uint      `json:"id" gorm:"primarykey"`
	UserID          string    `json:"user_id" gorm:"index"`
	FamilyGroup     string    `json:"family_group" gorm:"index"`
	Topic           string    `json:"topic" gorm:"index"` // e.g. "baby" | "house" | "finance" | "personal"
	EntityType      string    `json:"entity_type" gorm:"index"` // e.g. "baby" | "property" | "vehicle" | "person"
	EntityName      string    `json:"entity_name" gorm:"index"` // e.g. "Liam", "123 Main St", "Tesla Model 3"
	AttributesJSON  string    `json:"attributes_json" gorm:"type:jsonb"`
	UpdatedByUserID string    `json:"updated_by_user_id"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName explicitly specifies the database table name for GORM.
func (EntityProfile) TableName() string {
	return "entity_profiles"
}

// Attributes deserializes AttributesJSON into a map.
func (p *EntityProfile) Attributes() map[string]interface{} {
	if strings.TrimSpace(p.AttributesJSON) == "" {
		return make(map[string]interface{})
	}
	var attrs map[string]interface{}
	if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
		return make(map[string]interface{})
	}
	return attrs
}

// SetAttributes serializes an attribute map into AttributesJSON.
func (p *EntityProfile) SetAttributes(attrs map[string]interface{}) {
	if attrs == nil {
		p.AttributesJSON = "{}"
		return
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		p.AttributesJSON = "{}"
		return
	}
	p.AttributesJSON = string(b)
}

// MergeAttributes merges new key-value pairs into existing attributes.
func (p *EntityProfile) MergeAttributes(newAttrs map[string]interface{}) {
	current := p.Attributes()
	for k, v := range newAttrs {
		current[k] = v
	}
	p.SetAttributes(current)
}
