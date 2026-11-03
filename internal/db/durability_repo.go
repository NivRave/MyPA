package db

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
)

func init() {
	RegisterModel(
		&models.EncryptedCredential{},
		&models.MutationLog{},
		&models.IdempotencyRecord{},
	)
}

// SaveEncryptedCredential upserts an encrypted credential for a user and provider.
func (c *Client) SaveEncryptedCredential(cred models.EncryptedCredential) error {
	if cred.UpdatedAt.IsZero() {
		cred.UpdatedAt = time.Now()
	}
	result := c.DB.Save(&cred)
	if result.Error != nil {
		return fmt.Errorf("failed to save encrypted credential: %w", result.Error)
	}
	return nil
}

// GetEncryptedCredential retrieves an encrypted credential by user ID and provider.
func (c *Client) GetEncryptedCredential(userID, provider string) (*models.EncryptedCredential, error) {
	var cred models.EncryptedCredential
	result := c.DB.Where("user_id = ? AND provider = ?", userID, provider).First(&cred)
	if result.Error != nil {
		return nil, result.Error
	}
	return &cred, nil
}

// InsertMutationLog appends a new state mutation record to the audit log.
func (c *Client) InsertMutationLog(log models.MutationLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	result := c.DB.Create(&log)
	if result.Error != nil {
		return fmt.Errorf("failed to insert mutation log: %w", result.Error)
	}
	return nil
}

// GetMutationLogsByTopic retrieves recent mutation logs, optionally filtered by topic.
// If topic is empty or "all", all topics are returned.
func (c *Client) GetMutationLogsByTopic(topic string, limit int) ([]models.MutationLog, error) {
	if limit <= 0 {
		limit = 50
	}
	query := c.DB.Order("created_at desc").Limit(limit)
	if topic != "" && !strings.EqualFold(topic, "all") {
		query = query.Where("LOWER(topic) = ?", strings.ToLower(topic))
	}
	var logs []models.MutationLog
	result := query.Find(&logs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to query mutation logs: %w", result.Error)
	}
	return logs, nil
}

// GetMutationLogByID retrieves a specific mutation log by its unique ID.
func (c *Client) GetMutationLogByID(id string) (*models.MutationLog, error) {
	var log models.MutationLog
	result := c.DB.Where("id = ?", id).First(&log)
	if result.Error != nil {
		return nil, result.Error
	}
	return &log, nil
}

// GetLastMutation retrieves the most recent active (not undone) mutation for a user or family group.
func (c *Client) GetLastMutation(userID string, familyGroup string) (*models.MutationLog, error) {
	var log models.MutationLog
	query := c.DB.Where("is_undone = ?", false).Order("created_at desc")
	if familyGroup != "" && userID != "" {
		query = query.Where("user_id = ? OR family_group = ?", userID, familyGroup)
	} else if userID != "" {
		query = query.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		query = query.Where("family_group = ?", familyGroup)
	}
	result := query.First(&log)
	if result.Error != nil {
		return nil, result.Error
	}
	return &log, nil
}

// MarkMutationUndone marks a mutation log entry as having been rolled back.
func (c *Client) MarkMutationUndone(id string) error {
	result := c.DB.Model(&models.MutationLog{}).Where("id = ?", id).Update("is_undone", true)
	if result.Error != nil {
		return fmt.Errorf("failed to mark mutation undone: %w", result.Error)
	}
	return nil
}

// CheckIdempotency checks if an unexpired idempotency record exists for the given key.
func (c *Client) CheckIdempotency(key string) (*models.IdempotencyRecord, error) {
	var rec models.IdempotencyRecord
	result := c.DB.Where("key = ? AND expires_at > ?", key, time.Now()).First(&rec)
	if result.Error != nil {
		return nil, result.Error
	}
	return &rec, nil
}

// SaveIdempotency persists or updates an idempotency token with an expiration time.
func (c *Client) SaveIdempotency(rec models.IdempotencyRecord) error {
	if rec.ExpiresAt.IsZero() {
		rec.ExpiresAt = time.Now().Add(24 * time.Hour)
	}
	result := c.DB.Save(&rec)
	if result.Error != nil {
		return fmt.Errorf("failed to save idempotency record: %w", result.Error)
	}
	return nil
}

// ExportTopicData exports mutation logs and topic data as formatted JSON or CSV.
func (c *Client) ExportTopicData(topic string, format string, limit int) (string, error) {
	logs, err := c.GetMutationLogsByTopic(topic, limit)
	if err != nil {
		return "", err
	}

	normFormat := strings.ToLower(strings.TrimSpace(format))
	if normFormat == "csv" {
		var buf bytes.Buffer
		writer := csv.NewWriter(&buf)

		header := []string{"id", "timestamp", "user_id", "family_group", "topic", "action", "entity_type", "entity_id", "summary", "is_undone"}
		if err := writer.Write(header); err != nil {
			return "", fmt.Errorf("failed to write csv header: %w", err)
		}

		for _, item := range logs {
			row := []string{
				item.ID,
				item.CreatedAt.Format(time.RFC3339),
				item.UserID,
				item.FamilyGroup,
				item.Topic,
				item.Action,
				item.EntityType,
				item.EntityID,
				item.Summary,
				fmt.Sprintf("%t", item.IsUndone),
			}
			if err := writer.Write(row); err != nil {
				return "", fmt.Errorf("failed to write csv row: %w", err)
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return "", fmt.Errorf("failed to flush csv writer: %w", err)
		}
		return buf.String(), nil
	}

	// Default to JSON
	bytesData, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize topic data to json: %w", err)
	}
	return string(bytesData), nil
}
