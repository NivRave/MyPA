package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(&models.TopicMemory{}, &models.EntityProfile{})
}

// MemoryV2Repository defines the interface for managing topic-mapped memories and living entity profiles.
type MemoryV2Repository interface {
	SaveTopicMemory(ctx context.Context, memory *models.TopicMemory) error
	GetTopicMemory(ctx context.Context, id uint, familyGroup ...string) (*models.TopicMemory, error)
	DeleteTopicMemory(ctx context.Context, id uint, familyGroup ...string) error
	PinTopicMemory(ctx context.Context, id uint, isPinned bool, familyGroup ...string) (*models.TopicMemory, error)
	ListTopicMemories(ctx context.Context, familyGroup, topic string, includeSuperseded bool) ([]models.TopicMemory, error)
	SearchTopicMemories(ctx context.Context, userID, familyGroup, topic, query string, tags []string) ([]models.TopicMemory, error)
	SearchSimilarMemories(ctx context.Context, userID, familyGroup string, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error)
	SupersedeMemory(ctx context.Context, oldID uint, newID uint) error
	UpsertEntityProfile(ctx context.Context, profile *models.EntityProfile) error
	GetEntityProfile(ctx context.Context, familyGroup, topic, entityName string) (*models.EntityProfile, error)
	ListEntityProfiles(ctx context.Context, familyGroup, topic string) ([]models.EntityProfile, error)
	ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error)
}


// MemoryV2Repo implements MemoryV2Repository using GORM.
type MemoryV2Repo struct {
	db *gorm.DB
}

// NewMemoryV2Repo creates a new GORM-backed MemoryV2Repo.
func NewMemoryV2Repo(db *gorm.DB) *MemoryV2Repo {
	return &MemoryV2Repo{db: db}
}

// SaveTopicMemory inserts a new topic memory. If temporal bounds or timestamps are missing,
// appropriate defaults are applied.
func (r *MemoryV2Repo) SaveTopicMemory(ctx context.Context, memory *models.TopicMemory) error {
	if memory == nil {
		return errors.New("memory is nil")
	}

	if memory.ValidFrom.IsZero() {
		memory.ValidFrom = time.Now()
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}
	if memory.Scope == "" {
		memory.Scope = "personal"
	}
	if memory.TagsJSON == "" {
		memory.TagsJSON = "[]"
	}
	memory.Topic = strings.ToLower(strings.TrimSpace(memory.Topic))
	if len(memory.Embedding.Slice()) == 0 {
		memory.Embedding = pgvector.NewVector(make([]float32, 3072))
	}

	result := r.db.WithContext(ctx).Create(memory)
	if result.Error != nil {
		return fmt.Errorf("failed to save topic memory: %w", result.Error)
	}
	return nil
}

// SearchTopicMemories searches for active, valid topic memories accessible to the user
// based on user ID, family group, topic, keyword query, and tags.
func (r *MemoryV2Repo) SearchTopicMemories(ctx context.Context, userID, familyGroup, topic, query string, tags []string) ([]models.TopicMemory, error) {
	var memories []models.TopicMemory

	now := time.Now()
	dbQuery := r.db.WithContext(ctx).
		Where("superseded_by_id IS NULL AND (valid_until IS NULL OR valid_until > ?)", now)

	// User / Family access scope
	if familyGroup != "" && userID != "" {
		dbQuery = dbQuery.Where("((user_id = ? AND scope = 'personal') OR (family_group = ? AND scope = 'family') OR (user_id = ? AND scope = 'family'))",
			userID, familyGroup, userID)
	} else if familyGroup != "" {
		dbQuery = dbQuery.Where("(family_group = ? AND scope = 'family')", familyGroup)
	} else if userID != "" {
		dbQuery = dbQuery.Where("user_id = ?", userID)
	}

	// Topic filter
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic != "" {
		dbQuery = dbQuery.Where("LOWER(topic) = ?", topic)
	}

	// Text query search on fact or subtopic
	query = strings.TrimSpace(query)
	if query != "" {
		pattern := "%" + strings.ToLower(query) + "%"
		dbQuery = dbQuery.Where("(LOWER(fact) LIKE ? OR LOWER(subtopic) LIKE ?)", pattern, pattern)
	}

	// Tags filter
	for _, tag := range tags {
		normTag := strings.ToLower(strings.TrimSpace(tag))
		if normTag != "" {
			pattern := "%" + normTag + "%"
			dbQuery = dbQuery.Where("LOWER(tags_json) LIKE ?", pattern)
		}
	}

	// Order by pinned first, then newest
	result := dbQuery.Order("is_pinned DESC, created_at DESC").Find(&memories)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to search topic memories: %w", result.Error)
	}
	return memories, nil
}

// SearchSimilarMemories searches for active, valid topic memories using pgvector cosine distance,
// with transparent fallback to legacy memories if 0 results are found.
func (r *MemoryV2Repo) SearchSimilarMemories(ctx context.Context, userID, familyGroup string, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	var memories []models.TopicMemory
	if limit <= 0 {
		limit = 5
	}

	now := time.Now()
	// Order by cosine distance using the HNSW-indexed halfvec expression (P2-3). The whole query
	// is built from tx so it runs inside withVectorScan's session settings.
	err := withVectorScan(ctx, r.db, func(tx *gorm.DB) error {
		q := tx.Where("superseded_by_id IS NULL AND (valid_until IS NULL OR valid_until > ?)", now)
		if familyGroup != "" && userID != "" {
			q = q.Where("((user_id = ? AND scope = 'personal') OR (family_group = ? AND scope = 'family') OR (user_id = ? AND scope = 'family'))",
				userID, familyGroup, userID)
		} else if familyGroup != "" {
			q = q.Where("(family_group = ? AND scope = 'family')", familyGroup)
		} else if userID != "" {
			q = q.Where("user_id = ?", userID)
		}
		return q.Order(cosineOrder(embedding)).Limit(limit).Find(&memories).Error
	})
	if err != nil {
		return nil, err
	}

	// Transparent Fallback: If 0 results in topic_memories, query legacy memories table
	if len(memories) == 0 {
		var legacy []models.Memory
		searchIDs := []string{userID}
		if familyGroup != "" {
			searchIDs = append(searchIDs, familyGroup)
		}
		legErr := withVectorScan(ctx, r.db, func(tx *gorm.DB) error {
			return tx.Where("(user_id = ? AND scope = 'personal') OR (user_id IN ? AND scope = 'family')", userID, searchIDs).
				Order(cosineOrder(embedding)).
				Limit(limit).
				Find(&legacy).Error
		})
		if legErr == nil {
			for _, lm := range legacy {
				memories = append(memories, models.TopicMemory{
					UserID:    lm.UserID,
					Topic:     "general",
					Subtopic:  "fact",
					Fact:      lm.Fact,
					Scope:     lm.Scope,
					Embedding: lm.Embedding,
					CreatedAt: lm.CreatedAt,
				})
			}
		}
	}

	return memories, nil
}

// SupersedeMemory marks an existing memory as superseded by setting its SupersededByID
// pointer and setting its ValidUntil timestamp to the current time.
func (r *MemoryV2Repo) SupersedeMemory(ctx context.Context, oldID uint, newID uint) error {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.TopicMemory{}).
		Where("id = ?", oldID).
		Updates(map[string]interface{}{
			"superseded_by_id": newID,
			"valid_until":      now,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to supersede memory %d: %w", oldID, result.Error)
	}
	return nil
}

// UpsertEntityProfile inserts or updates a living structured EntityProfile.
// Profile matches on (Topic, EntityName) within the caller's FamilyGroup (or UserID).
func (r *MemoryV2Repo) UpsertEntityProfile(ctx context.Context, profile *models.EntityProfile) error {
	if profile == nil {
		return errors.New("profile is nil")
	}

	now := time.Now()
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = now
	}
	if profile.AttributesJSON == "" {
		profile.AttributesJSON = "{}"
	}
	profile.Topic = strings.ToLower(strings.TrimSpace(profile.Topic))
	profile.EntityType = strings.ToLower(strings.TrimSpace(profile.EntityType))
	profile.EntityName = strings.TrimSpace(profile.EntityName)

	query := r.db.WithContext(ctx).
		Where("LOWER(topic) = ? AND LOWER(entity_name) = ?", profile.Topic, strings.ToLower(profile.EntityName))

	if profile.FamilyGroup != "" {
		query = query.Where("family_group = ?", profile.FamilyGroup)
	} else if profile.UserID != "" {
		query = query.Where("user_id = ?", profile.UserID)
	}

	var existing models.EntityProfile
	err := query.First(&existing).Error
	if err == nil {
		// Update existing record preserving ID
		profile.ID = existing.ID
		result := r.db.WithContext(ctx).Save(profile)
		if result.Error != nil {
			return fmt.Errorf("failed to update entity profile: %w", result.Error)
		}
		return nil
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// Create new profile record
		result := r.db.WithContext(ctx).Create(profile)
		if result.Error != nil {
			return fmt.Errorf("failed to create entity profile: %w", result.Error)
		}
		return nil
	}
	return fmt.Errorf("failed to query existing entity profile: %w", err)
}

// GetEntityProfile retrieves an EntityProfile by topic and entity name for a family group.
func (r *MemoryV2Repo) GetEntityProfile(ctx context.Context, familyGroup, topic, entityName string) (*models.EntityProfile, error) {
	topic = strings.ToLower(strings.TrimSpace(topic))
	entityName = strings.ToLower(strings.TrimSpace(entityName))

	query := r.db.WithContext(ctx).
		Where("LOWER(topic) = ? AND LOWER(entity_name) = ?", topic, entityName)

	if familyGroup != "" {
		query = query.Where("(family_group = ? OR family_group = '')", familyGroup)
	}

	var profile models.EntityProfile
	err := query.First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get entity profile: %w", err)
	}
	return &profile, nil
}

// ListEntityProfiles retrieves all entity profiles for a family group or user, optionally filtered by topic.
func (r *MemoryV2Repo) ListEntityProfiles(ctx context.Context, familyGroup, topic string) ([]models.EntityProfile, error) {
	var profiles []models.EntityProfile

	query := r.db.WithContext(ctx)
	if familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup, familyGroup)
	} else {
		query = query.Where("1 = 0")
	}

	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic != "" && topic != "all" {
		query = query.Where("LOWER(topic) = ?", topic)
	}

	result := query.Order("updated_at DESC").Find(&profiles)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list entity profiles: %w", result.Error)
	}
	return profiles, nil
}

// ListActiveMemoriesForConsolidation returns all active topic memories ordered for consolidation.
func (r *MemoryV2Repo) ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error) {
	var memories []models.TopicMemory
	now := time.Now()

	result := r.db.WithContext(ctx).
		Where("superseded_by_id IS NULL AND (valid_until IS NULL OR valid_until > ?)", now).
		Order("topic ASC, subtopic ASC, created_at ASC").
		Find(&memories)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to list active memories for consolidation: %w", result.Error)
	}
	return memories, nil
}

// MemoryV2 returns a MemoryV2Repo instance using the Client's GORM DB.
func (c *Client) MemoryV2() *MemoryV2Repo {
	return NewMemoryV2Repo(c.DB)
}

// SaveTopicMemory provides a direct helper on Client.
func (c *Client) SaveTopicMemory(ctx context.Context, memory *models.TopicMemory) error {
	return c.MemoryV2().SaveTopicMemory(ctx, memory)
}

// SearchTopicMemories provides a direct helper on Client.
func (c *Client) SearchTopicMemories(ctx context.Context, userID, familyGroup, topic, query string, tags []string) ([]models.TopicMemory, error) {
	return c.MemoryV2().SearchTopicMemories(ctx, userID, familyGroup, topic, query, tags)
}

// SupersedeMemory provides a direct helper on Client.
func (c *Client) SupersedeMemory(ctx context.Context, oldID uint, newID uint) error {
	return c.MemoryV2().SupersedeMemory(ctx, oldID, newID)
}

// UpsertEntityProfile provides a direct helper on Client.
func (c *Client) UpsertEntityProfile(ctx context.Context, profile *models.EntityProfile) error {
	return c.MemoryV2().UpsertEntityProfile(ctx, profile)
}

// GetEntityProfile provides a direct helper on Client.
func (c *Client) GetEntityProfile(ctx context.Context, familyGroup, topic, entityName string) (*models.EntityProfile, error) {
	return c.MemoryV2().GetEntityProfile(ctx, familyGroup, topic, entityName)
}

// ListEntityProfiles provides a direct helper on Client.
func (c *Client) ListEntityProfiles(ctx context.Context, familyGroup, topic string) ([]models.EntityProfile, error) {
	return c.MemoryV2().ListEntityProfiles(ctx, familyGroup, topic)
}

// ListActiveMemoriesForConsolidation provides a direct helper on Client.
func (c *Client) ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error) {
	return c.MemoryV2().ListActiveMemoriesForConsolidation(ctx)
}

// SearchSimilarMemories provides a direct helper on Client.
func (c *Client) SearchSimilarMemories(ctx context.Context, userID, familyGroup string, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	return c.MemoryV2().SearchSimilarMemories(ctx, userID, familyGroup, embedding, limit)
}

// GetTopicMemory retrieves a TopicMemory by ID, optionally scoped by familyGroup.
func (r *MemoryV2Repo) GetTopicMemory(ctx context.Context, id uint, familyGroup ...string) (*models.TopicMemory, error) {
	var mem models.TopicMemory
	q := r.db.WithContext(ctx).Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		q = q.Where("family_group = ?", familyGroup[0])
	}
	if err := q.First(&mem).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &mem, nil
}

// DeleteTopicMemory deletes a TopicMemory by ID, optionally scoped by familyGroup.
func (r *MemoryV2Repo) DeleteTopicMemory(ctx context.Context, id uint, familyGroup ...string) error {
	q := r.db.WithContext(ctx).Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		q = q.Where("family_group = ?", familyGroup[0])
	}
	res := q.Delete(&models.TopicMemory{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("topic memory %d not found", id)
	}
	return nil
}

// PinTopicMemory toggles the pinned state of a TopicMemory.
func (r *MemoryV2Repo) PinTopicMemory(ctx context.Context, id uint, isPinned bool, familyGroup ...string) (*models.TopicMemory, error) {
	mem, err := r.GetTopicMemory(ctx, id, familyGroup...)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, fmt.Errorf("topic memory %d not found", id)
	}
	mem.IsPinned = isPinned
	if len(mem.Embedding.Slice()) == 0 {
		mem.Embedding = pgvector.NewVector(make([]float32, 3072))
	}
	if err := r.db.WithContext(ctx).Save(mem).Error; err != nil {
		return nil, err
	}
	return mem, nil
}

// ListTopicMemories retrieves topic memories optionally filtered by familyGroup and topic, with superseded filter.
func (r *MemoryV2Repo) ListTopicMemories(ctx context.Context, familyGroup, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	var mems []models.TopicMemory
	q := r.db.WithContext(ctx).Model(&models.TopicMemory{})
	if familyGroup != "" {
		q = q.Where("family_group = ?", familyGroup)
	}
	if topic != "" && !strings.EqualFold(topic, "all") {
		q = q.Where("LOWER(topic) = ?", strings.ToLower(strings.TrimSpace(topic)))
	}
	if !includeSuperseded {
		q = q.Where("superseded_by_id IS NULL")
	}
	if err := q.Order("is_pinned DESC, created_at DESC").Find(&mems).Error; err != nil {
		return nil, err
	}
	return mems, nil
}

// GetTopicMemory provides a direct helper on Client.
func (c *Client) GetTopicMemory(ctx context.Context, id uint, familyGroup ...string) (*models.TopicMemory, error) {
	return c.MemoryV2().GetTopicMemory(ctx, id, familyGroup...)
}

// DeleteTopicMemory provides a direct helper on Client.
func (c *Client) DeleteTopicMemory(ctx context.Context, id uint, familyGroup ...string) error {
	return c.MemoryV2().DeleteTopicMemory(ctx, id, familyGroup...)
}

// PinTopicMemory provides a direct helper on Client.
func (c *Client) PinTopicMemory(ctx context.Context, id uint, isPinned bool, familyGroup ...string) (*models.TopicMemory, error) {
	return c.MemoryV2().PinTopicMemory(ctx, id, isPinned, familyGroup...)
}

// ListTopicMemories provides a direct helper on Client.
func (c *Client) ListTopicMemories(ctx context.Context, familyGroup, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	return c.MemoryV2().ListTopicMemories(ctx, familyGroup, topic, includeSuperseded)
}


