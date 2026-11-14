package db

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
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
	if log.BeforeJSON == "" {
		log.BeforeJSON = "{}"
	}
	if log.AfterJSON == "" {
		log.AfterJSON = "{}"
	}
	result := c.DB.Create(&log)
	if result.Error != nil {
		return fmt.Errorf("failed to insert mutation log: %w", result.Error)
	}
	return nil
}

// GetMutationLogsByTopic retrieves recent mutation logs scoped to tenant, optionally filtered by topic.
// If topic is empty or "all", all topics are returned.
func (c *Client) GetMutationLogsByTopic(familyGroup, userID, topic string, limit int) ([]models.MutationLog, error) {
	if limit <= 0 {
		limit = 50
	}
	query := c.DB.Order("created_at desc").Limit(limit)
	query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup, userID)
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
func (c *Client) ExportTopicData(familyGroup, userID, topic string, format string, limit int) (string, error) {
	logs, err := c.GetMutationLogsByTopic(familyGroup, userID, topic, limit)
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

// RollbackSnapshot executes an atomic, transactional snapshot rollback of a mutation log.
func (c *Client) RollbackSnapshot(ctx context.Context, log models.MutationLog, tableName string) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch strings.ToLower(log.Action) {
		case "create":
			if log.EntityID == "" {
				return fmt.Errorf("cannot rollback create mutation without entity_id")
			}
			res := tx.Table(tableName).Where("id = ?", log.EntityID).Delete(nil)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback create on table %s: %w", tableName, res.Error)
			}
		case "update":
			if log.EntityID == "" || log.BeforeJSON == "" {
				return fmt.Errorf("cannot rollback update mutation without entity_id and before_json")
			}
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(log.BeforeJSON), &beforeMap); err != nil {
				return fmt.Errorf("failed to parse before_json for table %s: %w", tableName, err)
			}
			res := tx.Table(tableName).Where("id = ?", log.EntityID).Updates(beforeMap)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback update on table %s: %w", tableName, res.Error)
			}
		case "delete":
			if log.BeforeJSON == "" {
				return fmt.Errorf("cannot rollback delete mutation without before_json")
			}
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(log.BeforeJSON), &beforeMap); err != nil {
				return fmt.Errorf("failed to parse before_json for table %s: %w", tableName, err)
			}
			res := tx.Table(tableName).Create(beforeMap)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback delete on table %s: %w", tableName, res.Error)
			}
		default:
			return fmt.Errorf("unsupported mutation action %q for rollback", log.Action)
		}

		// Mark undone within the exact same database transaction
		res := tx.Model(&models.MutationLog{}).Where("id = ?", log.ID).Update("is_undone", true)
		if res.Error != nil {
			return fmt.Errorf("failed to mark mutation undone in transaction: %w", res.Error)
		}
		return nil
	})
}

