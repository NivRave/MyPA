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
	RegisterModel(&models.SharedSpace{}, &models.SharedTaskReminder{})
}

// SharedRepository manages database operations for shared multi-user spaces and tasks.
type SharedRepository struct {
	db *gorm.DB
}

// NewSharedRepository constructs a new SharedRepository.
func NewSharedRepository(db *gorm.DB) *SharedRepository {
	return &SharedRepository{db: db}
}

// UpsertSharedSpace creates or updates a SharedSpace record.
func (r *SharedRepository) UpsertSharedSpace(space *models.SharedSpace) error {
	if space == nil {
		return fmt.Errorf("shared space cannot be nil")
	}
	if space.FamilyGroup == "" {
		return fmt.Errorf("family_group is required")
	}
	if space.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	space.Topic = strings.ToLower(strings.TrimSpace(space.Topic))
	space.UpdatedAt = time.Now()

	var existing models.SharedSpace
	query := r.db.Where("family_group = ? AND topic = ?", space.FamilyGroup, space.Topic)
	if space.SharedWithUserID != "" && space.SharedWithUserID != "*" {
		query = query.Where("shared_with_user_id = ?", space.SharedWithUserID)
	}

	err := query.First(&existing).Error
	if err == nil {
		space.ID = existing.ID
		return r.db.Model(&existing).Updates(map[string]interface{}{
			"can_view":            space.CanView,
			"can_edit":            space.CanEdit,
			"notify_mode":         space.NotifyMode,
			"owner_user_id":       space.OwnerUserID,
			"shared_with_user_id": space.SharedWithUserID,
			"updated_at":          space.UpdatedAt,
		}).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Create(space).Error
	}
	return err
}

// GetSharedSpacesForFamily retrieves all configured shared spaces for a family group.
func (r *SharedRepository) GetSharedSpacesForFamily(familyGroup string) ([]models.SharedSpace, error) {
	var spaces []models.SharedSpace
	err := r.db.Where("family_group = ?", familyGroup).Order("topic ASC").Find(&spaces).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get shared spaces for family: %w", err)
	}
	return spaces, nil
}

// GetSharedSpace retrieves a shared space for a specific family group and topic.
// Returns (nil, nil) if no explicit configuration exists.
func (r *SharedRepository) GetSharedSpace(familyGroup, topic string) (*models.SharedSpace, error) {
	var space models.SharedSpace
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	err := r.db.Where("family_group = ? AND LOWER(topic) = ?", familyGroup, normTopic).First(&space).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get shared space: %w", err)
	}
	return &space, nil
}

// CreateSharedReminder inserts a new shared collaborative reminder or task.
func (r *SharedRepository) CreateSharedReminder(reminder *models.SharedTaskReminder) error {
	if reminder == nil {
		return fmt.Errorf("shared task reminder cannot be nil")
	}
	if reminder.FamilyGroup == "" {
		return fmt.Errorf("family_group is required")
	}
	if reminder.Title == "" {
		return fmt.Errorf("title is required")
	}
	if reminder.Topic == "" {
		reminder.Topic = models.TopicFamily
	}
	reminder.Topic = strings.ToLower(strings.TrimSpace(reminder.Topic))
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = time.Now()
	}
	return r.db.Create(reminder).Error
}

// CompleteSharedReminder marks a task as completed by completedByUserID.
func (r *SharedRepository) CompleteSharedReminder(id uint, completedByUserID string) (*models.SharedTaskReminder, error) {
	var reminder models.SharedTaskReminder
	if err := r.db.First(&reminder, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("shared reminder with id %d not found", id)
		}
		return nil, fmt.Errorf("failed to fetch shared reminder: %w", err)
	}

	now := time.Now()
	updates := map[string]interface{}{
		"is_completed":          true,
		"completed_by_user_id": completedByUserID,
		"completed_at":          &now,
	}

	if err := r.db.Model(&reminder).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to complete shared reminder: %w", err)
	}

	reminder.IsCompleted = true
	reminder.CompletedByUserID = completedByUserID
	reminder.CompletedAt = &now
	return &reminder, nil
}

// ListSharedReminders queries shared reminders for a family group, optionally filtering by topic.
func (r *SharedRepository) ListSharedReminders(familyGroup string, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error) {
	var reminders []models.SharedTaskReminder
	q := r.db.Where("family_group = ?", familyGroup)
	if topic != "" && topic != "*" {
		q = q.Where("LOWER(topic) = ?", strings.ToLower(strings.TrimSpace(topic)))
	}
	if !includeCompleted {
		q = q.Where("is_completed = ?", false)
	}
	err := q.Order("is_completed ASC, due_time ASC, created_at DESC").Find(&reminders).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list shared reminders: %w", err)
	}
	return reminders, nil
}

// Client extensions for Shared Repository

// UpsertSharedSpace creates or updates a SharedSpace on Client.
func (c *Client) UpsertSharedSpace(space *models.SharedSpace) error {
	return NewSharedRepository(c.DB).UpsertSharedSpace(space)
}

// GetSharedSpacesForFamily retrieves all shared spaces for a family on Client.
func (c *Client) GetSharedSpacesForFamily(familyGroup string) ([]models.SharedSpace, error) {
	return NewSharedRepository(c.DB).GetSharedSpacesForFamily(familyGroup)
}

// GetSharedSpace retrieves a shared space for a family group and topic on Client.
func (c *Client) GetSharedSpace(familyGroup, topic string) (*models.SharedSpace, error) {
	return NewSharedRepository(c.DB).GetSharedSpace(familyGroup, topic)
}

// CreateSharedReminder inserts a new shared reminder on Client.
func (c *Client) CreateSharedReminder(reminder *models.SharedTaskReminder) error {
	return NewSharedRepository(c.DB).CreateSharedReminder(reminder)
}

// CompleteSharedReminder marks a shared reminder as completed on Client.
func (c *Client) CompleteSharedReminder(id uint, completedByUserID string) (*models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).CompleteSharedReminder(id, completedByUserID)
}

// ListSharedReminders queries shared reminders on Client.
func (c *Client) ListSharedReminders(familyGroup string, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).ListSharedReminders(familyGroup, topic, includeCompleted)
}
