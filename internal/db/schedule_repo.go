package db

import (
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(
		&models.ScheduleEvent{},
		&models.TopicCalendarConfig{},
	)
}

// ScheduleRepository defines repository methods for topic-based schedule events and configurations.
type ScheduleRepository interface {
	// Schedule events are household rows (V4-30): every method takes the caller's scope.
	CreateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error
	GetScheduleEvent(s identity.Scope, id uint) (*models.ScheduleEvent, error)
	UpdateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error
	DeleteScheduleEvent(s identity.Scope, id uint) error
	// ListScheduleEvents returns the household's shared events plus the scope person's own.
	ListScheduleEvents(s identity.Scope, topic string, start, end time.Time) ([]models.ScheduleEvent, error)
	// Topic calendar configs are per person (no household_id) and keep their user key.
	UpsertTopicCalendarConfig(cfg *models.TopicCalendarConfig) error
	GetTopicCalendarConfig(userID, topic string) (*models.TopicCalendarConfig, error)
	ListTopicCalendarConfigs(userID string) ([]models.TopicCalendarConfig, error)
}

// NewScheduleRepository creates a ScheduleRepository backed by a *gorm.DB instance.
func NewScheduleRepository(db *gorm.DB) ScheduleRepository {
	return &Client{DB: db}
}

// CreateScheduleEvent inserts a new schedule event into the scope's household.
func (c *Client) CreateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error {
	if ev == nil {
		return fmt.Errorf("schedule event cannot be nil")
	}
	if err := stamp(s, &ev.HouseholdID, &ev.FamilyGroup); err != nil {
		return err
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	if ev.TagsJSON == "" {
		ev.TagsJSON = "[]"
	}
	ev.Topic = strings.ToLower(strings.TrimSpace(ev.Topic))

	result := c.DB.Create(ev)
	if result.Error != nil {
		return fmt.Errorf("failed to create schedule event: %w", result.Error)
	}
	return nil
}

// GetScheduleEvent fetches a schedule event of the scope's household by its ID.
// Returns (nil, nil) if the household has no such event.
func (c *Client) GetScheduleEvent(s identity.Scope, id uint) (*models.ScheduleEvent, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var ev models.ScheduleEvent
	result := q.Where("id = ?", id).First(&ev)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get schedule event: %w", result.Error)
	}
	return &ev, nil
}

// UpdateScheduleEvent updates an existing event of the scope's household. An event of another
// household is not found and is left untouched.
func (c *Client) UpdateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error {
	if ev == nil {
		return fmt.Errorf("schedule event cannot be nil")
	}
	q, err := c.scoped(s)
	if err != nil {
		return err
	}
	if err := stamp(s, &ev.HouseholdID, &ev.FamilyGroup); err != nil {
		return err
	}
	ev.Topic = strings.ToLower(strings.TrimSpace(ev.Topic))
	result := q.Model(&models.ScheduleEvent{}).Where("id = ?", ev.ID).Select("*").Omit("id", "created_at").Updates(ev)
	if result.Error != nil {
		return fmt.Errorf("failed to update schedule event: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("schedule event %d not found", ev.ID)
	}
	return nil
}

// DeleteScheduleEvent removes an event of the scope's household by ID. Deleting an ID the
// household does not have is a no-op, as deleting a missing ID was before.
func (c *Client) DeleteScheduleEvent(s identity.Scope, id uint) error {
	q, err := c.scoped(s)
	if err != nil {
		return err
	}
	result := q.Where("id = ?", id).Delete(&models.ScheduleEvent{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete schedule event: %w", result.Error)
	}
	return nil
}

// ListScheduleEvents retrieves the scope household's shared events plus the scope person's own
// events, matching the topic and time filters.
func (c *Client) ListScheduleEvents(s identity.Scope, topic string, start, end time.Time) ([]models.ScheduleEvent, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query := q.Model(&models.ScheduleEvent{})
	if userID := s.PersonKey(); userID != "" {
		where, args := personIs("user_id", "person_id", userID)
		query = query.Where("(is_shared = true OR "+where+")", args...)
	} else {
		query = query.Where("is_shared = true")
	}

	// Topic filter
	if topic != "" && !strings.EqualFold(topic, "all") {
		query = query.Where("LOWER(topic) = ?", strings.ToLower(strings.TrimSpace(topic)))
	}

	// Time range filter
	if !start.IsZero() && !end.IsZero() {
		query = query.Where("start_time <= ? AND (end_time >= ? OR (end_time IS NULL AND start_time >= ?))", end, start, start)
	} else if !start.IsZero() {
		query = query.Where("start_time >= ? OR end_time >= ?", start, start)
	} else if !end.IsZero() {
		query = query.Where("start_time <= ?", end)
	}

	var events []models.ScheduleEvent
	result := query.Order("start_time asc").Find(&events)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list schedule events: %w", result.Error)
	}
	return events, nil
}

// UpsertTopicCalendarConfig creates or updates the appearance and routing configuration for a topic.
func (c *Client) UpsertTopicCalendarConfig(cfg *models.TopicCalendarConfig) error {
	if cfg == nil {
		return fmt.Errorf("topic calendar config cannot be nil")
	}
	if cfg.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if cfg.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	cfg.Topic = strings.ToLower(strings.TrimSpace(cfg.Topic))
	cfg.UpdatedAt = time.Now()

	var existing models.TopicCalendarConfig
	where, args := personIs("user_id", "person_id", cfg.UserID)
	err := c.DB.Where(where, args...).Where("LOWER(topic) = ?", cfg.Topic).Order("person_id IS NULL").First(&existing).Error
	if err == nil {
		cfg.ID = existing.ID
		updates := map[string]interface{}{
			"color_id":                    cfg.ColorID,
			"prefix_tag":                  cfg.PrefixTag,
			"target_calendar_id":          cfg.TargetCalendarID,
			"include_in_morning_briefing": cfg.IncludeInMorningBriefing,
			"updated_at":                  cfg.UpdatedAt,
		}
		if cfg.SyncToGoogleDefault != nil {
			updates["sync_to_google_default"] = cfg.SyncToGoogleDefault
		}
		if err := c.DB.Model(&existing).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update topic calendar config: %w", err)
		}
		return nil
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := c.DB.Create(cfg).Error; err != nil {
			return fmt.Errorf("failed to create topic calendar config: %w", err)
		}
		return nil
	}
	return fmt.Errorf("failed to upsert topic calendar config: %w", err)
}

// GetTopicCalendarConfig retrieves a user's configuration for a specific topic.
// Returns (nil, nil) if no explicit configuration exists.
func (c *Client) GetTopicCalendarConfig(userID, topic string) (*models.TopicCalendarConfig, error) {
	var cfg models.TopicCalendarConfig
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	where, args := personIs("user_id", "person_id", userID)
	err := c.DB.Where(where, args...).Where("LOWER(topic) = ?", normTopic).Order("person_id IS NULL").First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get topic calendar config: %w", err)
	}
	return &cfg, nil
}

// ListTopicCalendarConfigs retrieves all topic calendar configurations for a user.
func (c *Client) ListTopicCalendarConfigs(userID string) ([]models.TopicCalendarConfig, error) {
	var configs []models.TopicCalendarConfig
	query := c.DB.Model(&models.TopicCalendarConfig{})
	if userID != "" {
		where, args := personIs("user_id", "person_id", userID)
		query = query.Where(where, args...)
	}
	result := query.Order("topic asc").Find(&configs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list topic calendar configs: %w", result.Error)
	}
	return configs, nil
}
