package db

import (
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/models"
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

	// Auto-migrate the AuditLog schema
	if err := db.AutoMigrate(&models.AuditLog{}); err != nil {
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
