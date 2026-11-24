package db

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// InsertMutationLog appends a new state mutation record to the audit log of the scope's
// household. household_id and the legacy family_group label come from the scope (V4-30).
func (c *Client) InsertMutationLog(s identity.Scope, log models.MutationLog) error {
	if err := stamp(s, &log.HouseholdID, &log.FamilyGroup); err != nil {
		return err
	}
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

// GetMutationLogsByTopic retrieves recent mutation logs of the scope's household, optionally
// filtered by topic. If topic is empty or "all", all topics are returned.
func (c *Client) GetMutationLogsByTopic(s identity.Scope, topic string, limit int) ([]models.MutationLog, error) {
	if limit <= 0 {
		limit = 50
	}
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = query.Order("created_at desc").Limit(limit)
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

// GetMutationLogByID retrieves a mutation log of the scope's household by its ID. A log of
// another household is not found (gorm.ErrRecordNotFound).
func (c *Client) GetMutationLogByID(s identity.Scope, id string) (*models.MutationLog, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var log models.MutationLog
	result := query.Where("id = ?", id).First(&log)
	if result.Error != nil {
		return nil, result.Error
	}
	return &log, nil
}

// GetLastMutation retrieves the most recent active (not undone) mutation of the scope's household.
func (c *Client) GetLastMutation(s identity.Scope) (*models.MutationLog, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var log models.MutationLog
	result := query.Where("is_undone = ?", false).Order("created_at desc").First(&log)
	if result.Error != nil {
		return nil, result.Error
	}
	return &log, nil
}

// GetLastUndoableMutation retrieves what /undo undoes: the newest active mutation of the scope's
// household that is the scope's person's own change (BL-65) and whose entity type is one of
// entityTypes, the types undo can roll back (BL-47: a change undo cannot roll back, such as an
// identity table's, never blocks it). Entity types are matched trimmed and ignoring case.
func (c *Client) GetLastUndoableMutation(s identity.Scope, entityTypes []string) (*models.MutationLog, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	if len(entityTypes) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	norm := make([]string, 0, len(entityTypes))
	for _, t := range entityTypes {
		norm = append(norm, strings.ToLower(strings.TrimSpace(t)))
	}
	own, args := ownMutations(s)
	var log models.MutationLog
	result := query.Where("is_undone = ?", false).
		Where("LOWER(BTRIM(entity_type)) IN ?", norm).
		Where(own, args...).
		Order("created_at desc").First(&log)
	if result.Error != nil {
		return nil, result.Error
	}
	return &log, nil
}

// ownMutations is the SQL form of MutationIsOwn: the scope's person's changes by person_id, or by
// the old key where person_id is empty. A scope without a person has no changes of its own.
func ownMutations(s identity.Scope) (string, []interface{}) {
	key, pid := strings.TrimSpace(s.PersonKey()), strings.TrimSpace(s.PersonID())
	switch {
	case pid != "":
		return "(person_id = ? OR (person_id IS NULL AND user_id IN (?, ?)))", []interface{}{pid, key, pid}
	case key != "":
		return "(person_id IS NULL AND user_id = ?)", []interface{}{key}
	default:
		return "FALSE", nil
	}
}

// MarkMutationUndone marks a mutation log entry of the scope's household as rolled back.
func (c *Client) MarkMutationUndone(s identity.Scope, id string) error {
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	result := query.Model(&models.MutationLog{}).Where("id = ?", id).Update("is_undone", true)
	if result.Error != nil {
		return fmt.Errorf("failed to mark mutation undone: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to mark mutation undone: mutation %s not found in this household", id)
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

// ExportTopicData exports the scope's household's mutation logs as formatted JSON or CSV.
func (c *Client) ExportTopicData(s identity.Scope, topic string, format string, limit int) (string, error) {
	logs, err := c.GetMutationLogsByTopic(s, topic, limit)
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

// RollbackSnapshot executes an atomic, transactional snapshot rollback of a mutation log of the
// scope's household (V4-30). Only the log's ID is taken from the caller: the log itself is read
// inside the transaction, and must be an active log of the scope's household. Every statement on
// the target table is confined to that household (or, for a person-keyed table without
// household_id, to the log's own user), and a statement that matches no row fails the whole
// rollback. A restored snapshot always lands in the scope's household: snapshots written before
// V4-30 carry no household_id, and a crafted one must never move a row into another household.
//
// Whose change it is and whose row it touches are checked here too, on the locked log and on the
// row as it is and as it would be restored (MayUndo, BL-65): via says where the undo was asked
// from. A memory brought back by undo never gets a vector from a snapshot: a snapshot's vector
// belongs to whichever model made it (BL-57). A restored memory, or one whose text the undo
// changes, gets a zero vector, which `mypa embeddings reembed --missing` fills in the current
// model; an undo that leaves the text as it is keeps the row's current vector.
func (c *Client) RollbackSnapshot(ctx context.Context, s identity.Scope, requested models.MutationLog, tableName string, via models.UndoVia) error {
	if !s.Valid() {
		return identity.ErrNoScope
	}
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var log models.MutationLog
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND household_id = ? AND is_undone = ?", requested.ID, s.HouseholdID(), false).
			First(&log).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("permission denied: mutation %q is not an active mutation of this household", requested.ID)
		}
		if err != nil {
			return fmt.Errorf("failed to load mutation %s: %w", requested.ID, err)
		}
		if err := MayUndo(s, via, log, tableName); err != nil {
			return err
		}
		householdScoped := tx.Migrator().HasColumn(tableName, "household_id")
		if !householdScoped && !tx.Migrator().HasColumn(tableName, "user_id") {
			return fmt.Errorf("table %s has neither household_id nor user_id; refusing to roll back", tableName)
		}
		target := func() *gorm.DB {
			q := tx.Table(tableName)
			if householdScoped {
				return q.Where("household_id = ?", s.HouseholdID())
			}
			return q.Where("user_id = ?", log.UserID)
		}
		confine := func(m map[string]interface{}) {
			if householdScoped {
				m["household_id"] = s.HouseholdID()
				m["family_group"] = s.LegacyLabel() // every household table still carries the label (until V4-44)
				return
			}
			m["user_id"] = log.UserID
		}

		// current is the row as it is now, locked until the rollback commits so that whose it is
		// cannot change between the check and the write; a row missing from this household fails
		// the rollback.
		current := func(op string) (UndoRowState, error) {
			row := map[string]interface{}{}
			err := target().Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", log.EntityID).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed to rollback %s on table %s: row %s not found in this household", op, tableName, log.EntityID)
			}
			if err != nil {
				return nil, fmt.Errorf("failed to rollback %s on table %s: %w", op, tableName, err)
			}
			return row, nil
		}

		switch strings.ToLower(log.Action) {
		case "create":
			if log.EntityID == "" {
				return fmt.Errorf("cannot rollback create mutation without entity_id")
			}
			cur, err := current("create")
			if err != nil {
				return err
			}
			if err := MayUndo(s, via, log, tableName, cur); err != nil {
				return err
			}
			res := target().Where("id = ?", log.EntityID).Delete(nil)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback create on table %s: %w", tableName, res.Error)
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("failed to rollback create on table %s: row %s not found in this household", tableName, log.EntityID)
			}
		case "update":
			if log.EntityID == "" || log.BeforeJSON == "" {
				return fmt.Errorf("cannot rollback update mutation without entity_id and before_json")
			}
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(log.BeforeJSON), &beforeMap); err != nil {
				return fmt.Errorf("failed to parse before_json for table %s: %w", tableName, err)
			}
			delete(beforeMap, "id") // the row being restored is the log's entity, never another one
			confine(beforeMap)
			cur, err := current("update")
			if err != nil {
				return err
			}
			restored := UndoRowState{}
			for k, v := range cur {
				restored[k] = v
			}
			for k, v := range beforeMap {
				restored[k] = v
			}
			if err := MayUndo(s, via, log, tableName, cur, restored); err != nil {
				return err
			}
			if tableName == embeddedTable {
				delete(beforeMap, "embedding")
				if fact, ok := beforeMap["fact"]; ok && text(fact) != text(cur["fact"]) {
					if err := markNeedsEmbedding(tx, beforeMap); err != nil {
						return err
					}
				}
			}
			res := target().Where("id = ?", log.EntityID).Updates(beforeMap)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback update on table %s: %w", tableName, res.Error)
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("failed to rollback update on table %s: row %s not found in this household", tableName, log.EntityID)
			}
		case "delete":
			if log.BeforeJSON == "" {
				return fmt.Errorf("cannot rollback delete mutation without before_json")
			}
			var beforeMap map[string]interface{}
			if err := json.Unmarshal([]byte(log.BeforeJSON), &beforeMap); err != nil {
				return fmt.Errorf("failed to parse before_json for table %s: %w", tableName, err)
			}
			confine(beforeMap)
			if err := MayUndo(s, via, log, tableName, beforeMap); err != nil {
				return err
			}
			if tableName == embeddedTable {
				if err := markNeedsEmbedding(tx, beforeMap); err != nil {
					return err
				}
			}
			res := tx.Table(tableName).Create(beforeMap)
			if res.Error != nil {
				return fmt.Errorf("failed to rollback delete on table %s: %w", tableName, res.Error)
			}
		default:
			return fmt.Errorf("unsupported mutation action %q for rollback", log.Action)
		}

		// Mark undone within the exact same database transaction (the row is locked above).
		res := tx.Model(&models.MutationLog{}).Where("id = ? AND household_id = ?", log.ID, s.HouseholdID()).Update("is_undone", true)
		if res.Error != nil {
			return fmt.Errorf("failed to mark mutation undone in transaction: %w", res.Error)
		}
		return nil
	})
}

// markNeedsEmbedding replaces whatever vector a memory snapshot holds with "no embedding yet": a
// zero vector of the stored size (models.TopicMemory.BeforeCreate), which `mypa embeddings
// reembed --missing` fills in the current model (BL-57).
func markNeedsEmbedding(tx *gorm.DB, row map[string]interface{}) error {
	dims, err := embeddingColumnDimensions(tx)
	if err != nil {
		return err
	}
	row["embedding"] = pgvector.NewVector(make([]float32, dims))
	return nil
}
