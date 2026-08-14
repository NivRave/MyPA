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
	if err := db.AutoMigrate(&models.AuditLog{}, &models.Memory{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	slog.Info("connected to postgres and ran migrations successfully")

	return &Client{DB: db}, nil
}

// LogInteraction saves a new audit log record to the database.
func (c *Client) LogInteraction(log models.AuditLog) error {
	result := c.DB.Create(&log)
	if result.Error != nil {
		return fmt.Errorf("failed to insert audit log: %w", result.Error)
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

// GetUniqueUsers returns a list of unique user IDs from the audit logs.
func (c *Client) GetUniqueUsers() ([]string, error) {
	var userIDs []string
	result := c.DB.Model(&models.AuditLog{}).Distinct("user_id").Pluck("user_id", &userIDs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get unique users: %w", result.Error)
	}
	return userIDs, nil
}

// GetLastAuditLogForUser returns the most recent audit log for a given user.
func (c *Client) GetLastAuditLogForUser(userID string) (*models.AuditLog, error) {
	var log models.AuditLog
	result := c.DB.Where("user_id = ?", userID).Order("created_at desc").First(&log)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get last audit log: %w", result.Error)
	}
	return &log, nil
}
