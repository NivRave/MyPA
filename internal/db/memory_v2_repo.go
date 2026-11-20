package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
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
//
// Topic memories and entity profiles are household rows (V4-30): every method takes the caller's
// scope, except ListActiveMemoriesForConsolidation, which scans every household for the
// scheduled consolidation job.
type MemoryV2Repository interface {
	SaveTopicMemory(ctx context.Context, s identity.Scope, memory *models.TopicMemory) error
	GetTopicMemory(ctx context.Context, s identity.Scope, id uint) (*models.TopicMemory, error)
	DeleteTopicMemory(ctx context.Context, s identity.Scope, id uint) error
	PinTopicMemory(ctx context.Context, s identity.Scope, id uint, isPinned bool) (*models.TopicMemory, error)
	ListTopicMemories(ctx context.Context, s identity.Scope, topic string, includeSuperseded bool) ([]models.TopicMemory, error)
	SearchTopicMemories(ctx context.Context, s identity.Scope, topic, query string, tags []string) ([]models.TopicMemory, error)
	SearchSimilarMemories(ctx context.Context, s identity.Scope, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error)
	SupersedeMemory(ctx context.Context, s identity.Scope, oldID uint, newID uint) error
	UpsertEntityProfile(ctx context.Context, s identity.Scope, profile *models.EntityProfile) error
	GetEntityProfile(ctx context.Context, s identity.Scope, topic, entityName string) (*models.EntityProfile, error)
	ListEntityProfiles(ctx context.Context, s identity.Scope, topic string) ([]models.EntityProfile, error)
	ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error)
}

// memoryVisibility is who may see a memory inside the household: the household's family-scope
// memories, plus the scope person's own personal ones.
func memoryVisibility(q *gorm.DB, s identity.Scope) *gorm.DB {
	if p := s.PersonKey(); p != "" {
		where, args := personIs("user_id", "person_id", p)
		return q.Where("(scope = 'family' OR (scope = 'personal' AND "+where+"))", args...)
	}
	return q.Where("scope = 'family'")
}

// MemoryV2Repo implements MemoryV2Repository using GORM.
type MemoryV2Repo struct {
	db *gorm.DB
}

// NewMemoryV2Repo creates a new GORM-backed MemoryV2Repo.
func NewMemoryV2Repo(db *gorm.DB) *MemoryV2Repo {
	return &MemoryV2Repo{db: db}
}

func (r *MemoryV2Repo) scoped(ctx context.Context, s identity.Scope) (*gorm.DB, error) {
	q, err := (&Client{DB: r.db}).scoped(s)
	if err != nil {
		return nil, err
	}
	return q.WithContext(ctx), nil
}

// SaveTopicMemory inserts a new topic memory into the scope's household. If temporal bounds or
// timestamps are missing, appropriate defaults are applied.
func (r *MemoryV2Repo) SaveTopicMemory(ctx context.Context, s identity.Scope, memory *models.TopicMemory) error {
	if memory == nil {
		return errors.New("memory is nil")
	}
	if err := stamp(s, &memory.HouseholdID, &memory.FamilyGroup); err != nil {
		return err
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
		memory.Embedding = pgvector.NewVector(make([]float32, models.EmbeddingDimensions))
	}

	result := r.db.WithContext(ctx).Create(memory)
	if result.Error != nil {
		return fmt.Errorf("failed to save topic memory: %w", result.Error)
	}
	return nil
}

// SearchTopicMemories searches for active, valid topic memories the scope may see (the
// household's family memories and the person's own personal ones), by topic, keyword query and
// tags.
func (r *MemoryV2Repo) SearchTopicMemories(ctx context.Context, s identity.Scope, topic, query string, tags []string) ([]models.TopicMemory, error) {
	var memories []models.TopicMemory

	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	dbQuery := memoryVisibility(q.Where("superseded_by_id IS NULL AND (valid_until IS NULL OR valid_until > ?)", now), s)

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

// SearchSimilarMemories searches for active, valid topic memories the scope may see (the
// household's family memories and the person's own personal ones) by pgvector cosine distance.
func (r *MemoryV2Repo) SearchSimilarMemories(ctx context.Context, s identity.Scope, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	var memories []models.TopicMemory
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	if limit <= 0 {
		limit = 5
	}

	now := time.Now()
	// Order by cosine distance using the HNSW-indexed halfvec expression (P2-3). The whole query
	// is built from tx so it runs inside withVectorScan's session settings.
	err := withVectorScan(ctx, r.db, func(tx *gorm.DB) error {
		q := tx.Where("household_id = ?", s.HouseholdID()).
			Where("superseded_by_id IS NULL AND (valid_until IS NULL OR valid_until > ?)", now)
		q = memoryVisibility(q, s)
		return q.Order(cosineOrder(embedding)).Limit(limit).Find(&memories).Error
	})
	if err != nil {
		return nil, err
	}
	// No legacy `memories` fallback (P2-4): MigrateLegacyMemories copies v1 rows at startup.
	return memories, nil
}

// SupersedeMemory marks an existing memory as superseded by setting its SupersededByID
// pointer and setting its ValidUntil timestamp to the current time.
// Both memories must belong to the scope's household.
func (r *MemoryV2Repo) SupersedeMemory(ctx context.Context, s identity.Scope, oldID uint, newID uint) error {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	var n int64
	if err := r.db.WithContext(ctx).Model(&models.TopicMemory{}).Where("household_id = ? AND id = ?", s.HouseholdID(), newID).Count(&n).Error; err != nil {
		return fmt.Errorf("failed to check superseding memory %d: %w", newID, err)
	}
	if n == 0 {
		return fmt.Errorf("topic memory %d not found", newID)
	}
	now := time.Now()
	result := q.
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
// Profile matches on (Topic, EntityName) within the scope's household.
func (r *MemoryV2Repo) UpsertEntityProfile(ctx context.Context, s identity.Scope, profile *models.EntityProfile) error {
	if profile == nil {
		return errors.New("profile is nil")
	}
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	if err := stamp(s, &profile.HouseholdID, &profile.FamilyGroup); err != nil {
		return err
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

	query := q.Where("LOWER(topic) = ? AND LOWER(entity_name) = ?", profile.Topic, strings.ToLower(profile.EntityName))

	var existing models.EntityProfile
	err = query.First(&existing).Error
	if err == nil {
		// Update existing record preserving ID, inside the household only
		profile.ID = existing.ID
		result := r.db.WithContext(ctx).Model(&models.EntityProfile{}).
			Where("id = ? AND household_id = ?", existing.ID, s.HouseholdID()).
			Select("*").Omit("id").Updates(profile)
		if result.Error != nil {
			return fmt.Errorf("failed to update entity profile: %w", result.Error)
		}
		return nil
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// Create new profile record
		profile.ID = 0
		result := r.db.WithContext(ctx).Create(profile)
		if result.Error != nil {
			return fmt.Errorf("failed to create entity profile: %w", result.Error)
		}
		return nil
	}
	return fmt.Errorf("failed to query existing entity profile: %w", err)
}

// GetEntityProfile retrieves the scope household's EntityProfile by topic and entity name.
func (r *MemoryV2Repo) GetEntityProfile(ctx context.Context, s identity.Scope, topic, entityName string) (*models.EntityProfile, error) {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	topic = strings.ToLower(strings.TrimSpace(topic))
	entityName = strings.ToLower(strings.TrimSpace(entityName))

	query := q.Where("LOWER(topic) = ? AND LOWER(entity_name) = ?", topic, entityName)

	var profile models.EntityProfile
	err = query.First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get entity profile: %w", err)
	}
	return &profile, nil
}

// ListEntityProfiles retrieves the scope household's entity profiles, optionally filtered by topic.
func (r *MemoryV2Repo) ListEntityProfiles(ctx context.Context, s identity.Scope, topic string) ([]models.EntityProfile, error) {
	var profiles []models.EntityProfile

	query, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
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
// It scans every household (scheduled job): the consolidator groups rows by household_id and
// builds each group's scope from it (identity.ForStoredRow) before writing.
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
func (c *Client) SaveTopicMemory(ctx context.Context, s identity.Scope, memory *models.TopicMemory) error {
	return c.MemoryV2().SaveTopicMemory(ctx, s, memory)
}

// SearchTopicMemories provides a direct helper on Client.
func (c *Client) SearchTopicMemories(ctx context.Context, s identity.Scope, topic, query string, tags []string) ([]models.TopicMemory, error) {
	return c.MemoryV2().SearchTopicMemories(ctx, s, topic, query, tags)
}

// SupersedeMemory provides a direct helper on Client.
func (c *Client) SupersedeMemory(ctx context.Context, s identity.Scope, oldID uint, newID uint) error {
	return c.MemoryV2().SupersedeMemory(ctx, s, oldID, newID)
}

// UpsertEntityProfile provides a direct helper on Client.
func (c *Client) UpsertEntityProfile(ctx context.Context, s identity.Scope, profile *models.EntityProfile) error {
	return c.MemoryV2().UpsertEntityProfile(ctx, s, profile)
}

// GetEntityProfile provides a direct helper on Client.
func (c *Client) GetEntityProfile(ctx context.Context, s identity.Scope, topic, entityName string) (*models.EntityProfile, error) {
	return c.MemoryV2().GetEntityProfile(ctx, s, topic, entityName)
}

// ListEntityProfiles provides a direct helper on Client.
func (c *Client) ListEntityProfiles(ctx context.Context, s identity.Scope, topic string) ([]models.EntityProfile, error) {
	return c.MemoryV2().ListEntityProfiles(ctx, s, topic)
}

// ListActiveMemoriesForConsolidation provides a direct helper on Client.
func (c *Client) ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error) {
	return c.MemoryV2().ListActiveMemoriesForConsolidation(ctx)
}

// SearchSimilarMemories provides a direct helper on Client.
func (c *Client) SearchSimilarMemories(ctx context.Context, s identity.Scope, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	return c.MemoryV2().SearchSimilarMemories(ctx, s, embedding, limit)
}

// GetTopicMemory retrieves a TopicMemory of the scope's household by ID.
func (r *MemoryV2Repo) GetTopicMemory(ctx context.Context, s identity.Scope, id uint) (*models.TopicMemory, error) {
	var mem models.TopicMemory
	q, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	if err := q.Where("id = ?", id).First(&mem).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &mem, nil
}

// DeleteTopicMemory deletes a TopicMemory of the scope's household by ID.
func (r *MemoryV2Repo) DeleteTopicMemory(ctx context.Context, s identity.Scope, id uint) error {
	q, err := r.scoped(ctx, s)
	if err != nil {
		return err
	}
	res := q.Where("id = ?", id).Delete(&models.TopicMemory{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("topic memory %d not found", id)
	}
	return nil
}

// PinTopicMemory toggles the pinned state of a TopicMemory of the scope's household.
func (r *MemoryV2Repo) PinTopicMemory(ctx context.Context, s identity.Scope, id uint, isPinned bool) (*models.TopicMemory, error) {
	mem, err := r.GetTopicMemory(ctx, s, id)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, fmt.Errorf("topic memory %d not found", id)
	}
	res := r.db.WithContext(ctx).Model(&models.TopicMemory{}).
		Where("id = ? AND household_id = ?", mem.ID, s.HouseholdID()).
		Update("is_pinned", isPinned)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("topic memory %d not found", id)
	}
	mem.IsPinned = isPinned
	return mem, nil
}

// ListTopicMemories retrieves the scope household's topic memories, optionally filtered by topic,
// with a superseded filter.
func (r *MemoryV2Repo) ListTopicMemories(ctx context.Context, s identity.Scope, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	var mems []models.TopicMemory
	sq, err := r.scoped(ctx, s)
	if err != nil {
		return nil, err
	}
	q := sq.Model(&models.TopicMemory{})
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
func (c *Client) GetTopicMemory(ctx context.Context, s identity.Scope, id uint) (*models.TopicMemory, error) {
	return c.MemoryV2().GetTopicMemory(ctx, s, id)
}

// DeleteTopicMemory provides a direct helper on Client.
func (c *Client) DeleteTopicMemory(ctx context.Context, s identity.Scope, id uint) error {
	return c.MemoryV2().DeleteTopicMemory(ctx, s, id)
}

// PinTopicMemory provides a direct helper on Client.
func (c *Client) PinTopicMemory(ctx context.Context, s identity.Scope, id uint, isPinned bool) (*models.TopicMemory, error) {
	return c.MemoryV2().PinTopicMemory(ctx, s, id, isPinned)
}

// ListTopicMemories provides a direct helper on Client.
func (c *Client) ListTopicMemories(ctx context.Context, s identity.Scope, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	return c.MemoryV2().ListTopicMemories(ctx, s, topic, includeSuperseded)
}
