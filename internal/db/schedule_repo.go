package db

import (
	"errors"
	"fmt"
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
	CreateScheduleEvent(ev *models.ScheduleEvent) error
	GetScheduleEvent(id uint, familyGroup ...string) (*models.ScheduleEvent, error)
	UpdateScheduleEvent(ev *models.ScheduleEvent) error
	DeleteScheduleEvent(id uint, familyGroup ...string) error
	ListScheduleEvents(userID, familyGroup, topic string, start, end time.Time) ([]models.ScheduleEvent, error)
	UpsertTopicCalendarConfig(cfg *models.TopicCalendarConfig) error
	GetTopicCalendarConfig(userID, topic string) (*models.TopicCalendarConfig, error)
	ListTopicCalendarConfigs(userID string) ([]models.TopicCalendarConfig, error)
}

// NewScheduleRepository creates a ScheduleRepository backed by a *gorm.DB instance.
func NewScheduleRepository(db *gorm.DB) ScheduleRepository {
	return &Client{DB: db}
}

// CreateScheduleEvent inserts a new schedule event into the database.
func (c *Client) CreateScheduleEvent(ev *models.ScheduleEvent) error {
	if ev == nil {
		return fmt.Errorf("schedule event cannot be nil")
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

// GetScheduleEvent fetches a single schedule event by its ID, optionally scoped to tenant.
// Returns (nil, nil) if the event is not found.
func (c *Client) GetScheduleEvent(id uint, familyGroup ...string) (*models.ScheduleEvent, error) {
	var ev models.ScheduleEvent
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.First(&ev)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get schedule event: %w", result.Error)
	}
	return &ev, nil
}

// UpdateScheduleEvent updates an existing schedule event.
func (c *Client) UpdateScheduleEvent(ev *models.ScheduleEvent) error {
	if ev == nil {
		return fmt.Errorf("schedule event cannot be nil")
	}
	ev.Topic = strings.ToLower(strings.TrimSpace(ev.Topic))
	result := c.DB.Save(ev)
	if result.Error != nil {
		return fmt.Errorf("failed to update schedule event: %w", result.Error)
	}
	return nil
}

// DeleteScheduleEvent removes a schedule event by ID, optionally scoped to tenant.
func (c *Client) DeleteScheduleEvent(id uint, familyGroup ...string) error {
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.Delete(&models.ScheduleEvent{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete schedule event: %w", result.Error)
	}
	return nil
}

// ListScheduleEvents retrieves schedule events matching the specified filters.
func (c *Client) ListScheduleEvents(userID, familyGroup, topic string, start, end time.Time) ([]models.ScheduleEvent, error) {
	query := c.DB.Model(&models.ScheduleEvent{})

	// User / FamilyGroup filter
	if userID != "" && familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '' AND is_shared = true) OR user_id = ?)", familyGroup, userID)
	} else if userID != "" {
		query = query.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		query = query.Where("family_group = ? AND family_group != ''", familyGroup)
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
	err := c.DB.Where("user_id = ? AND LOWER(topic) = ?", cfg.UserID, cfg.Topic).First(&existing).Error
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
	err := c.DB.Where("user_id = ? AND LOWER(topic) = ?", userID, normTopic).First(&cfg).Error
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
		query = query.Where("user_id = ?", userID)
	}
	result := query.Order("topic asc").Find(&configs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list topic calendar configs: %w", result.Error)
	}
	return configs, nil
}
