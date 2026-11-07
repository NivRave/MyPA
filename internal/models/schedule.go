package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ScheduleEvent represents a topic-tagged event in the multi-schedule engine.
// It supports both internal-only schedules (e.g. baby feeds, routine reminders)
// and Google Calendar synchronized events (virtual sub-calendars, partner calendar sync).
type ScheduleEvent struct {
	ID                    uint      `json:"id" gorm:"primaryKey"`
	ExternalGoogleEventID string    `json:"external_google_event_id" gorm:"index"`
	PartnerGoogleEventID  string    `json:"partner_google_event_id" gorm:"index"`
	UserID                string    `json:"user_id" gorm:"index;index:idx_schedule_events_user_start,priority:1"`
	FamilyGroup           string    `json:"family_group" gorm:"index;index:idx_schedule_events_family_start,priority:1"`
	Topic                 string    `json:"topic" gorm:"index"`
	TagsJSON              string    `json:"tags_json" gorm:"type:jsonb"`
	Title                 string    `json:"title"`
	Description           string    `json:"description"`
	StartTime             time.Time `json:"start_time" gorm:"index;index:idx_schedule_events_family_start,priority:2,sort:asc;index:idx_schedule_events_user_start,priority:2,sort:asc"`
	EndTime               time.Time `json:"end_time" gorm:"index"`
	RecurrenceRule        string    `json:"recurrence_rule"`
	SyncToGoogle          bool      `json:"sync_to_google" gorm:"default:true"`
	IsShared              bool      `json:"is_shared" gorm:"default:false"`
	CreatedByUserID       string    `json:"created_by_user_id"`
	CreatedAt             time.Time `json:"created_at"`
}

// TopicCalendarConfig defines per-topic appearance and routing preferences
// for virtual sub-calendars on Google Calendar and morning briefings.
type TopicCalendarConfig struct {
	ID                       uint      `json:"id" gorm:"primaryKey"`
	UserID                   string    `json:"user_id" gorm:"index:idx_user_topic,unique"`
	Topic                    string    `json:"topic" gorm:"index:idx_user_topic,unique"`
	ColorID                  string    `json:"color_id"`
	PrefixTag                string    `json:"prefix_tag"`
	TargetCalendarID         string    `json:"target_calendar_id"`
	IncludeInMorningBriefing bool      `json:"include_in_morning_briefing" gorm:"default:true"`
	SyncToGoogleDefault      *bool     `json:"sync_to_google_default,omitempty"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// Tags deserializes the TagsJSON array into a slice of strings.
func (e *ScheduleEvent) Tags() []string {
	if strings.TrimSpace(e.TagsJSON) == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(e.TagsJSON), &tags); err != nil {
		return []string{}
	}
	return tags
}

// SetTags serializes a slice of strings into TagsJSON.
func (e *ScheduleEvent) SetTags(tags []string) error {
	if len(tags) == 0 {
		e.TagsJSON = "[]"
		return nil
	}
	bytes, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}
	e.TagsJSON = string(bytes)
	return nil
}

// FormattedTitle formats the title with virtual sub-calendar prefix bracket tag (e.g. "[Baby] Pediatrician Checkup").
// If the title is already tagged with a bracket prefix, it returns the title as-is.
func (e *ScheduleEvent) FormattedTitle(prefixTag string) string {
	tag := strings.TrimSpace(prefixTag)
	if tag == "" {
		topic := strings.TrimSpace(e.Topic)
		if topic == "" {
			return e.Title
		}
		tag = strings.ToUpper(topic[:1]) + strings.ToLower(topic[1:])
	}

	cleanTitle := strings.TrimSpace(e.Title)
	if strings.HasPrefix(cleanTitle, "[") && strings.Contains(cleanTitle, "]") {
		return cleanTitle
	}
	return fmt.Sprintf("[%s] %s", tag, cleanTitle)
}
