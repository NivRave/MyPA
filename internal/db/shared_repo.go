package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(&models.SharedSpace{}, &models.SharedTaskReminder{})
}

// SharedRepository manages database operations for shared multi-user spaces and tasks. Shared
// spaces and shared tasks are household rows (V4-30): every method but the scheduler's scan
// takes the caller's scope.
type SharedRepository struct {
	db *gorm.DB
}

// NewSharedRepository constructs a new SharedRepository.
func NewSharedRepository(db *gorm.DB) *SharedRepository {
	return &SharedRepository{db: db}
}

func (r *SharedRepository) scoped(s identity.Scope) (*gorm.DB, error) {
	return (&Client{DB: r.db}).scoped(s)
}

// UpsertSharedSpace creates or updates a SharedSpace record of the scope's household.
func (r *SharedRepository) UpsertSharedSpace(s identity.Scope, space *models.SharedSpace) error {
	if space == nil {
		return fmt.Errorf("shared space cannot be nil")
	}
	if space.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	q, err := r.scoped(s)
	if err != nil {
		return err
	}
	if err := stamp(s, &space.HouseholdID, &space.FamilyGroup); err != nil {
		return err
	}
	space.Topic = strings.ToLower(strings.TrimSpace(space.Topic))
	space.UpdatedAt = time.Now()

	var existing models.SharedSpace
	query := q.Where("topic = ?", space.Topic)
	if space.SharedWithUserID != "" && space.SharedWithUserID != "*" {
		query = query.Where("shared_with_user_id = ?", space.SharedWithUserID)
	}

	err = query.First(&existing).Error
	if err == nil {
		space.ID = existing.ID
		return r.db.Model(&models.SharedSpace{}).Where("id = ? AND household_id = ?", existing.ID, s.HouseholdID()).Updates(map[string]interface{}{
			"can_view":            space.CanView,
			"can_edit":            space.CanEdit,
			"notify_mode":         space.NotifyMode,
			"owner_user_id":       space.OwnerUserID,
			"shared_with_user_id": space.SharedWithUserID,
			"updated_at":          space.UpdatedAt,
		}).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		space.ID = 0
		return r.db.Create(space).Error
	}
	return err
}

// GetSharedSpacesForFamily retrieves all configured shared spaces of the scope's household.
func (r *SharedRepository) GetSharedSpacesForFamily(s identity.Scope) ([]models.SharedSpace, error) {
	q, err := r.scoped(s)
	if err != nil {
		return nil, err
	}
	var spaces []models.SharedSpace
	if err := q.Order("topic ASC").Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("failed to get shared spaces for family: %w", err)
	}
	return spaces, nil
}

// GetSharedSpace retrieves the scope household's shared space for a topic.
// Returns (nil, nil) if no explicit configuration exists.
func (r *SharedRepository) GetSharedSpace(s identity.Scope, topic string) (*models.SharedSpace, error) {
	q, err := r.scoped(s)
	if err != nil {
		return nil, err
	}
	var space models.SharedSpace
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	err = q.Where("LOWER(topic) = ?", normTopic).First(&space).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get shared space: %w", err)
	}
	return &space, nil
}

// CreateSharedReminder inserts a new shared collaborative reminder or task into the scope's household.
func (r *SharedRepository) CreateSharedReminder(s identity.Scope, reminder *models.SharedTaskReminder) error {
	if reminder == nil {
		return fmt.Errorf("shared task reminder cannot be nil")
	}
	if err := stamp(s, &reminder.HouseholdID, &reminder.FamilyGroup); err != nil {
		return err
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

// CompleteSharedReminder marks a task of the scope's household as completed by completedByUserID.
func (r *SharedRepository) CompleteSharedReminder(s identity.Scope, id uint, completedByUserID string) (*models.SharedTaskReminder, error) {
	q, err := r.scoped(s)
	if err != nil {
		return nil, err
	}
	var reminder models.SharedTaskReminder
	if err := q.Where("id = ?", id).First(&reminder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("shared reminder with id %d not found", id)
		}
		return nil, fmt.Errorf("failed to fetch shared reminder: %w", err)
	}

	now := time.Now()
	updates := map[string]interface{}{
		"is_completed":         true,
		"completed_by_user_id": completedByUserID,
		"completed_at":         &now,
	}

	res := r.db.Model(&models.SharedTaskReminder{}).Where("id = ? AND household_id = ?", reminder.ID, s.HouseholdID()).Updates(updates)
	if res.Error != nil {
		return nil, fmt.Errorf("failed to complete shared reminder: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("shared reminder with id %d not found", id)
	}

	reminder.IsCompleted = true
	reminder.CompletedByUserID = completedByUserID
	reminder.CompletedAt = &now
	return &reminder, nil
}

// GetSharedReminder retrieves a shared reminder of the scope's household by ID. Returns (nil, nil) if not found.
func (r *SharedRepository) GetSharedReminder(s identity.Scope, id uint) (*models.SharedTaskReminder, error) {
	q, err := r.scoped(s)
	if err != nil {
		return nil, err
	}
	var reminder models.SharedTaskReminder
	result := q.Where("id = ?", id).First(&reminder)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get shared reminder %d: %w", id, result.Error)
	}
	return &reminder, nil
}

// ListSharedReminders queries the scope household's shared reminders, optionally filtering by topic.
func (r *SharedRepository) ListSharedReminders(s identity.Scope, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error) {
	q, err := r.scoped(s)
	if err != nil {
		return nil, err
	}
	var reminders []models.SharedTaskReminder
	if topic != "" && topic != "*" && !strings.EqualFold(topic, "all") {
		q = q.Where("LOWER(topic) = ?", strings.ToLower(strings.TrimSpace(topic)))
	}
	if !includeCompleted {
		q = q.Where("is_completed = ?", false)
	}
	err = q.Order("is_completed ASC, due_time ASC, created_at DESC").Find(&reminders).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list shared reminders: %w", err)
	}
	return reminders, nil
}

// Client extensions for Shared Repository

// UpsertSharedSpace creates or updates a SharedSpace on Client.
func (c *Client) UpsertSharedSpace(s identity.Scope, space *models.SharedSpace) error {
	return NewSharedRepository(c.DB).UpsertSharedSpace(s, space)
}

// GetSharedSpacesForFamily retrieves all shared spaces of the scope's household on Client.
func (c *Client) GetSharedSpacesForFamily(s identity.Scope) ([]models.SharedSpace, error) {
	return NewSharedRepository(c.DB).GetSharedSpacesForFamily(s)
}

// GetSharedSpace retrieves the scope household's shared space for a topic on Client.
func (c *Client) GetSharedSpace(s identity.Scope, topic string) (*models.SharedSpace, error) {
	return NewSharedRepository(c.DB).GetSharedSpace(s, topic)
}

// CreateSharedReminder inserts a new shared reminder on Client.
func (c *Client) CreateSharedReminder(s identity.Scope, reminder *models.SharedTaskReminder) error {
	return NewSharedRepository(c.DB).CreateSharedReminder(s, reminder)
}

// CompleteSharedReminder marks a shared reminder as completed on Client.
func (c *Client) CompleteSharedReminder(s identity.Scope, id uint, completedByUserID string) (*models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).CompleteSharedReminder(s, id, completedByUserID)
}

// GetSharedReminder retrieves a shared reminder on Client.
func (c *Client) GetSharedReminder(s identity.Scope, id uint) (*models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).GetSharedReminder(s, id)
}

// ListSharedReminders queries shared reminders on Client.
func (c *Client) ListSharedReminders(s identity.Scope, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).ListSharedReminders(s, topic, includeCompleted)
}

// GetDueSharedReminders returns uncompleted, unsent shared reminders that have reached their due
// time. It scans every household (scheduled job): each row's household_id becomes the scope
// that sends and marks it (identity.ForStoredRow).
func (r *SharedRepository) GetDueSharedReminders() ([]models.SharedTaskReminder, error) {
	var reminders []models.SharedTaskReminder
	now := time.Now()
	err := r.db.Where("is_completed = ? AND is_sent = ? AND due_time IS NOT NULL AND due_time <= ?", false, false, now).Find(&reminders).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get due shared reminders: %w", err)
	}
	return reminders, nil
}

// MarkSharedReminderSent sets is_sent = true and reminded_at to now for a shared reminder of the
// scope's household.
func (r *SharedRepository) MarkSharedReminderSent(s identity.Scope, id uint) error {
	q, err := r.scoped(s)
	if err != nil {
		return err
	}
	now := time.Now()
	return q.Model(&models.SharedTaskReminder{}).Where("id = ?", id).Updates(map[string]interface{}{
		"is_sent":     true,
		"reminded_at": &now,
	}).Error
}

// GetDueSharedReminders returns uncompleted, unsent shared reminders on Client. It scans every
// household (scheduled job).
func (c *Client) GetDueSharedReminders() ([]models.SharedTaskReminder, error) {
	return NewSharedRepository(c.DB).GetDueSharedReminders()
}

// MarkSharedReminderSent marks a shared reminder of the scope's household as sent on Client.
func (c *Client) MarkSharedReminderSent(s identity.Scope, id uint) error {
	return NewSharedRepository(c.DB).MarkSharedReminderSent(s, id)
}
