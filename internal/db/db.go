package db

import (
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Client handles database operations.
type Client struct {
	DB *gorm.DB
}

// NewClient initializes the database connection and runs auto-migrations.
func NewClient(dsn string) (*Client, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Ensure pgvector extension exists
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return nil, fmt.Errorf("failed to create vector extension: %w", err)
	}

	// Auto-migrate schemas
	if err := db.AutoMigrate(&models.AuditSession{}, &models.AuditEvent{}, &models.Memory{}, &models.ScheduledReminder{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	slog.Info("connected to postgres and ran migrations successfully")

	return &Client{DB: db}, nil
}

// InsertAuditSession saves a new audit session to the database.
func (c *Client) InsertAuditSession(session models.AuditSession) error {
	result := c.DB.Create(&session)
	if result.Error != nil {
		return fmt.Errorf("failed to insert audit session: %w", result.Error)
	}
	return nil
}

// InsertAuditEvent saves a new audit event to the database.
func (c *Client) InsertAuditEvent(event models.AuditEvent) error {
	result := c.DB.Create(&event)
	if result.Error != nil {
		return fmt.Errorf("failed to insert audit event: %w", result.Error)
	}
	return nil
}

// SaveMemory stores a semantic memory fact and its vector embedding.
func (c *Client) SaveMemory(memory models.Memory) error {
	result := c.DB.Create(&memory)
	if result.Error != nil {
		return fmt.Errorf("failed to insert memory: %w", result.Error)
	}
	return nil
}

// SearchMemories finds the most relevant memories using cosine distance.
func (c *Client) SearchMemories(userID string, embedding pgvector.Vector, limit int) ([]models.Memory, error) {
	var memories []models.Memory
	// <=> is the cosine distance operator in pgvector
	result := c.DB.Where("user_id = ?", userID).
		Order(gorm.Expr("embedding <=> ?", embedding)).
		Limit(limit).
		Find(&memories)
	
	if result.Error != nil {
		return nil, fmt.Errorf("failed to search memories: %w", result.Error)
	}
	return memories, nil
}

// GetUniqueUsers returns a list of unique user IDs from the audit sessions.
func (c *Client) GetUniqueUsers() ([]string, error) {
	var userIDs []string
	result := c.DB.Model(&models.AuditSession{}).Distinct("user_id").Pluck("user_id", &userIDs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get unique users: %w", result.Error)
	}
	return userIDs, nil
}

// GetLastAuditSessionForUser returns the most recent audit session for a given user.
func (c *Client) GetLastAuditSessionForUser(userID string) (*models.AuditSession, error) {
	var session models.AuditSession
	result := c.DB.Where("user_id = ?", userID).Order("start_time desc").First(&session)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get last audit session: %w", result.Error)
	}
	return &session, nil
}

// SaveReminder creates a new scheduled reminder.
func (c *Client) SaveReminder(reminder models.ScheduledReminder) error {
	result := c.DB.Create(&reminder)
	if result.Error != nil {
		return fmt.Errorf("failed to insert reminder: %w", result.Error)
	}
	return nil
}

// GetDueReminders fetches reminders that are due to be sent (due_time <= now) and haven't been sent yet.
func (c *Client) GetDueReminders() ([]models.ScheduledReminder, error) {
	var reminders []models.ScheduledReminder
	// Fetch where due_time is less than or equal to now, and is_sent is false.
	result := c.DB.Where("is_sent = ? AND due_time <= ?", false, "now()").Find(&reminders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to fetch due reminders: %w", result.Error)
	}
	return reminders, nil
}

// MarkReminderSent marks a scheduled reminder as sent.
func (c *Client) MarkReminderSent(id uint) error {
	result := c.DB.Model(&models.ScheduledReminder{}).Where("id = ?", id).Update("is_sent", true)
	if result.Error != nil {
		return fmt.Errorf("failed to mark reminder as sent: %w", result.Error)
	}
	return nil
}
