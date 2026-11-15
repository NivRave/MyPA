package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryRepo provides a thread-safe in-memory implementation of db.MemoryV2Repository for unit testing.
type inMemoryRepo struct {
	mu             sync.RWMutex
	nextMemoryID   uint
	nextProfileID  uint
	topicMemories  []models.TopicMemory
	entityProfiles []models.EntityProfile
}

func newInMemoryRepo() *inMemoryRepo {
	return &inMemoryRepo{
		nextMemoryID:   1,
		nextProfileID:  1,
		topicMemories:  make([]models.TopicMemory, 0),
		entityProfiles: make([]models.EntityProfile, 0),
	}
}

func (r *inMemoryRepo) SaveTopicMemory(ctx context.Context, mem *models.TopicMemory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if mem.ID == 0 {
		mem.ID = r.nextMemoryID
		r.nextMemoryID++
	}
	if mem.ValidFrom.IsZero() {
		mem.ValidFrom = time.Now()
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now()
	}
	if mem.Scope == "" {
		mem.Scope = "personal"
	}
	if mem.TagsJSON == "" {
		mem.TagsJSON = "[]"
	}
	mem.Topic = strings.ToLower(strings.TrimSpace(mem.Topic))
	r.topicMemories = append(r.topicMemories, *mem)
	return nil
}

func (r *inMemoryRepo) SearchTopicMemories(ctx context.Context, userID, familyGroup, topic, query string, tags []string) ([]models.TopicMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.TopicMemory
	now := time.Now()
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	normQuery := strings.ToLower(strings.TrimSpace(query))

	for _, m := range r.topicMemories {
		// Active check
		if m.SupersededByID != nil {
			continue
		}
		if m.ValidUntil != nil && m.ValidUntil.Before(now) {
			continue
		}

		// Scope / access check
		if familyGroup != "" && userID != "" {
			canAccess := (m.UserID == userID && m.Scope == "personal") ||
				(m.FamilyGroup == familyGroup && m.Scope == "family") ||
				(m.UserID == userID && m.Scope == "family")
			if !canAccess {
				continue
			}
		} else if familyGroup != "" {
			if m.FamilyGroup != familyGroup || m.Scope != "family" {
				continue
			}
		} else if userID != "" {
			if m.UserID != userID {
				continue
			}
		}

		// Topic filter
		if normTopic != "" && strings.ToLower(m.Topic) != normTopic {
			continue
		}

		// Text query filter
		if normQuery != "" {
			match := strings.Contains(strings.ToLower(m.Fact), normQuery) ||
				strings.Contains(strings.ToLower(m.Subtopic), normQuery)
			if !match {
				continue
			}
		}

		// Tags filter
		if len(tags) > 0 {
			memTags := m.Tags()
			tagMatch := true
			for _, requiredTag := range tags {
				found := false
				for _, mt := range memTags {
					if strings.EqualFold(mt, requiredTag) {
						found = true
						break
					}
				}
				if !found {
					tagMatch = false
					break
				}
			}
			if !tagMatch {
				continue
			}
		}

		result = append(result, m)
	}

	// Sort pinned first, then newest
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsPinned != result[j].IsPinned {
			return result[i].IsPinned
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

func (r *inMemoryRepo) SearchSimilarMemories(ctx context.Context, userID, familyGroup string, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.TopicMemory
	now := time.Now()
	for _, m := range r.topicMemories {
		if m.SupersededByID != nil {
			continue
		}
		if m.ValidUntil != nil && m.ValidUntil.Before(now) {
			continue
		}
		if familyGroup != "" && userID != "" {
			canAccess := (m.UserID == userID && m.Scope == "personal") ||
				(m.FamilyGroup == familyGroup && m.Scope == "family") ||
				(m.UserID == userID && m.Scope == "family")
			if !canAccess {
				continue
			}
		} else if familyGroup != "" {
			if m.FamilyGroup != familyGroup || m.Scope != "family" {
				continue
			}
		} else if userID != "" {
			if m.UserID != userID {
				continue
			}
		}
		result = append(result, m)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (r *inMemoryRepo) SupersedeMemory(ctx context.Context, oldID uint, newID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for i := range r.topicMemories {
		if r.topicMemories[i].ID == oldID {
			r.topicMemories[i].SupersededByID = &newID
			r.topicMemories[i].ValidUntil = &now
			return nil
		}
	}
	return nil
}

func (r *inMemoryRepo) UpsertEntityProfile(ctx context.Context, profile *models.EntityProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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

	for i, existing := range r.entityProfiles {
		match := strings.EqualFold(existing.Topic, profile.Topic) &&
			strings.EqualFold(existing.EntityName, profile.EntityName)
		scopeMatch := false
		if profile.FamilyGroup != "" {
			scopeMatch = existing.FamilyGroup == profile.FamilyGroup
		} else {
			scopeMatch = existing.UserID == profile.UserID
		}

		if match && scopeMatch {
			profile.ID = existing.ID
			r.entityProfiles[i] = *profile
			return nil
		}
	}

	profile.ID = r.nextProfileID
	r.nextProfileID++
	r.entityProfiles = append(r.entityProfiles, *profile)
	return nil
}

func (r *inMemoryRepo) GetEntityProfile(ctx context.Context, familyGroup, topic, entityName string) (*models.EntityProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normTopic := strings.ToLower(strings.TrimSpace(topic))
	normName := strings.ToLower(strings.TrimSpace(entityName))

	for _, p := range r.entityProfiles {
		if strings.ToLower(p.Topic) == normTopic && strings.ToLower(p.EntityName) == normName {
			if familyGroup == "" || p.FamilyGroup == familyGroup || p.FamilyGroup == "" {
				copyProfile := p
				return &copyProfile, nil
			}
		}
	}
	return nil, nil
}

func (r *inMemoryRepo) ListEntityProfiles(ctx context.Context, familyGroup, topic string) ([]models.EntityProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.EntityProfile
	normTopic := strings.ToLower(strings.TrimSpace(topic))

	for _, p := range r.entityProfiles {
		if familyGroup != "" && p.FamilyGroup != familyGroup {
			continue
		}
		if normTopic != "" && strings.ToLower(p.Topic) != normTopic {
			continue
		}
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})

	return result, nil
}

func (r *inMemoryRepo) ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.TopicMemory
	now := time.Now()

	for _, m := range r.topicMemories {
		if m.SupersededByID != nil {
			continue
		}
		if m.ValidUntil != nil && m.ValidUntil.Before(now) {
			continue
		}
		result = append(result, m)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Topic != result[j].Topic {
			return result[i].Topic < result[j].Topic
		}
		if result[i].Subtopic != result[j].Subtopic {
			return result[i].Subtopic < result[j].Subtopic
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return result, nil
}

func (r *inMemoryRepo) GetTopicMemory(ctx context.Context, id uint, familyGroup ...string) (*models.TopicMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.topicMemories {
		if m.ID == id {
			if len(familyGroup) > 0 && familyGroup[0] != "" && m.FamilyGroup != familyGroup[0] {
				return nil, nil
			}
			copyMem := m
			return &copyMem, nil
		}
	}
	return nil, nil
}

func (r *inMemoryRepo) DeleteTopicMemory(ctx context.Context, id uint, familyGroup ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, m := range r.topicMemories {
		if m.ID == id {
			if len(familyGroup) > 0 && familyGroup[0] != "" && m.FamilyGroup != familyGroup[0] {
				return fmt.Errorf("topic memory %d not found", id)
			}
			r.topicMemories = append(r.topicMemories[:i], r.topicMemories[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("topic memory %d not found", id)
}

func (r *inMemoryRepo) PinTopicMemory(ctx context.Context, id uint, isPinned bool, familyGroup ...string) (*models.TopicMemory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, m := range r.topicMemories {
		if m.ID == id {
			if len(familyGroup) > 0 && familyGroup[0] != "" && m.FamilyGroup != familyGroup[0] {
				return nil, fmt.Errorf("topic memory %d not found", id)
			}
			r.topicMemories[i].IsPinned = isPinned
			copyMem := r.topicMemories[i]
			return &copyMem, nil
		}
	}
	return nil, fmt.Errorf("topic memory %d not found", id)
}

func (r *inMemoryRepo) ListTopicMemories(ctx context.Context, familyGroup, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []models.TopicMemory
	normTopic := strings.ToLower(strings.TrimSpace(topic))
	for _, m := range r.topicMemories {
		if !includeSuperseded && m.SupersededByID != nil {
			continue
		}
		if familyGroup != "" && m.FamilyGroup != familyGroup {
			continue
		}
		if normTopic != "" && !strings.EqualFold(normTopic, "all") && strings.ToLower(m.Topic) != normTopic {
			continue
		}
		result = append(result, m)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------


func TestMemoryV2Repository_CRUD(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()

	// 1. SaveTopicMemory
	mem1 := models.TopicMemory{
		UserID:          "user-1",
		FamilyGroup:     "fam-1",
		Topic:           models.TopicBaby,
		Subtopic:        "allergies",
		Fact:            "Liam is allergic to eggs",
		Scope:           "family",
		CreatedByUserID: "user-1",
		IsPinned:        true,
	}
	mem1.SetTags([]string{"allergy", "diet", "liam"})
	err := repo.SaveTopicMemory(ctx, &mem1)
	require.NoError(t, err)
	assert.Equal(t, uint(1), mem1.ID)
	assert.True(t, mem1.IsActive())
	assert.Equal(t, []string{"allergy", "diet", "liam"}, mem1.Tags())

	// 2. SearchTopicMemories by topic and tag
	results, err := repo.SearchTopicMemories(ctx, "user-1", "fam-1", models.TopicBaby, "", []string{"allergy"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Liam is allergic to eggs", results[0].Fact)
	assert.True(t, results[0].IsPinned)

	// 3. Supersede older memory
	mem2 := models.TopicMemory{
		UserID:          "user-1",
		FamilyGroup:     "fam-1",
		Topic:           models.TopicBaby,
		Subtopic:        "allergies",
		Fact:            "Liam has outgrown egg allergy per Dr. Smith",
		Scope:           "family",
		CreatedByUserID: "user-1",
	}
	err = repo.SaveTopicMemory(ctx, &mem2)
	require.NoError(t, err)

	err = repo.SupersedeMemory(ctx, mem1.ID, mem2.ID)
	require.NoError(t, err)

	// Search again - only mem2 should appear
	active, err := repo.SearchTopicMemories(ctx, "user-1", "fam-1", models.TopicBaby, "", nil)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, mem2.ID, active[0].ID)
	assert.Equal(t, "Liam has outgrown egg allergy per Dr. Smith", active[0].Fact)

	// 4. EntityProfile Upsert and Get
	profile := models.EntityProfile{
		UserID:          "user-1",
		FamilyGroup:     "fam-1",
		Topic:           models.TopicBaby,
		EntityType:      models.EntityTypeBaby,
		EntityName:      "Liam",
		UpdatedByUserID: "user-1",
	}
	profile.SetAttributes(map[string]interface{}{
		"birth_date": "2024-05-10",
		"weight_kg":  7.5,
	})
	err = repo.UpsertEntityProfile(ctx, &profile)
	require.NoError(t, err)
	assert.Equal(t, uint(1), profile.ID)

	// Update existing profile with new attribute
	profile.MergeAttributes(map[string]interface{}{
		"weight_kg": 7.8,
		"height_cm": 68.0,
	})
	err = repo.UpsertEntityProfile(ctx, &profile)
	require.NoError(t, err)
	assert.Equal(t, uint(1), profile.ID) // ID preserved

	// Fetch profile
	fetched, err := repo.GetEntityProfile(ctx, "fam-1", models.TopicBaby, "Liam")
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, "Liam", fetched.EntityName)
	attrs := fetched.Attributes()
	assert.Equal(t, 7.8, attrs["weight_kg"])
	assert.Equal(t, 68.0, attrs["height_cm"])

	// List profiles
	profiles, err := repo.ListEntityProfiles(ctx, "fam-1", models.TopicBaby)
	require.NoError(t, err)
	assert.Len(t, profiles, 1)
}

func TestToolModule_Declarations(t *testing.T) {
	repo := newInMemoryRepo()
	mod := memory.NewModule(repo)

	assert.Equal(t, memory.ModuleName, mod.Name())
	decls := mod.Declarations()
	require.Len(t, decls, 5)

	toolNames := make(map[string]bool)
	for _, d := range decls {
		toolNames[d.Name] = true
	}
	assert.True(t, toolNames["save_topic_memory"])
	assert.True(t, toolNames["search_topic_memories"])
	assert.True(t, toolNames["upsert_entity_profile"])
	assert.True(t, toolNames["get_entity_profile"])
	assert.True(t, toolNames["remember_fact"])
}

func TestToolModule_Execute_SaveAndSearch(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	mod := memory.NewModule(repo)

	var recordedEvents []events.MutationEvent
	var eventMu sync.Mutex
	bus.Subscribe(func(ctx context.Context, e events.MutationEvent) error {
		eventMu.Lock()
		defer eventMu.Unlock()
		recordedEvents = append(recordedEvents, e)
		return nil
	})

	callCtx := orchestrator.CallContext{
		User: &models.User{
			ID:          "u-123",
			PlatformID:  "tg-123",
			FamilyGroup: "Smiths",
		},
		EventBus: bus,
	}

	// 1. Save memory
	saveArgs := map[string]interface{}{
		"topic":     "house",
		"subtopic":  "rent",
		"fact":      "Monthly rent is $2,200 due on 1st",
		"scope":     "family",
		"tags":      []interface{}{"lease", "housing", "finance"},
		"is_pinned": true,
	}

	resp, err := mod.Execute(ctx, callCtx, "save_topic_memory", saveArgs)
	require.NoError(t, err)

	var saveRes map[string]interface{}
	err = json.Unmarshal([]byte(resp), &saveRes)
	require.NoError(t, err)
	assert.Equal(t, "saved", saveRes["status"])
	assert.Equal(t, "house", saveRes["topic"])
	assert.Equal(t, true, saveRes["is_pinned"])

	// Check mutation event emitted
	eventMu.Lock()
	require.Len(t, recordedEvents, 1)
	assert.Equal(t, "memory", recordedEvents[0].Topic)
	assert.Equal(t, "create", recordedEvents[0].Action)
	assert.Equal(t, "TopicMemory", recordedEvents[0].EntityType)
	assert.Equal(t, "Smiths", recordedEvents[0].FamilyGroup)
	eventMu.Unlock()

	// 2. Search memories
	searchArgs := map[string]interface{}{
		"topic": "house",
		"query": "rent",
	}
	searchResp, err := mod.Execute(ctx, callCtx, "search_topic_memories", searchArgs)
	require.NoError(t, err)

	var searchRes map[string]interface{}
	err = json.Unmarshal([]byte(searchResp), &searchRes)
	require.NoError(t, err)
	assert.Equal(t, float64(1), searchRes["count"])

	// 3. Save newer memory that supersedes older memory
	saveArgs2 := map[string]interface{}{
		"topic":                "house",
		"subtopic":             "rent",
		"fact":                 "Rent increased to $2,400 per renewal notice",
		"scope":                "family",
		"supersedes_memory_id": saveRes["id"],
	}
	resp2, err := mod.Execute(ctx, callCtx, "save_topic_memory", saveArgs2)
	require.NoError(t, err)
	var saveRes2 map[string]interface{}
	_ = json.Unmarshal([]byte(resp2), &saveRes2)
	assert.Equal(t, saveRes["id"], saveRes2["superseded_memory_id"])

	// Search again - only the $2,400 fact should appear
	searchResp2, err := mod.Execute(ctx, callCtx, "search_topic_memories", map[string]interface{}{"topic": "house"})
	require.NoError(t, err)
	var searchRes2 map[string]interface{}
	_ = json.Unmarshal([]byte(searchResp2), &searchRes2)
	assert.Equal(t, float64(1), searchRes2["count"])
	memList := searchRes2["memories"].([]interface{})
	firstMem := memList[0].(map[string]interface{})
	assert.Equal(t, "Rent increased to $2,400 per renewal notice", firstMem["fact"])
}

func TestToolModule_Execute_EntityProfiles(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	mod := memory.NewModule(repo)

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg-user",
			FamilyGroup: "FamilyA",
		},
		EventBus: bus,
	}

	// 1. Upsert Profile
	upsertArgs := map[string]interface{}{
		"topic":       "baby",
		"entity_type": "baby",
		"entity_name": "Liam",
		"attributes": map[string]interface{}{
			"birth_date": "2024-05-10",
			"weight_kg":  7.6,
		},
	}
	res, err := mod.Execute(ctx, callCtx, "upsert_entity_profile", upsertArgs)
	require.NoError(t, err)

	var upMap map[string]interface{}
	err = json.Unmarshal([]byte(res), &upMap)
	require.NoError(t, err)
	assert.Equal(t, "upserted", upMap["status"])
	assert.Equal(t, "Liam", upMap["entity_name"])

	// 2. Get Profile
	getArgs := map[string]interface{}{
		"topic":       "baby",
		"entity_name": "Liam",
	}
	getRes, err := mod.Execute(ctx, callCtx, "get_entity_profile", getArgs)
	require.NoError(t, err)

	var getMap map[string]interface{}
	err = json.Unmarshal([]byte(getRes), &getMap)
	require.NoError(t, err)
	assert.Equal(t, "Liam", getMap["entity_name"])
	attrs := getMap["attributes"].(map[string]interface{})
	assert.Equal(t, "2024-05-10", attrs["birth_date"])
	assert.Equal(t, 7.6, attrs["weight_kg"])

	// 3. Get non-existent profile
	missingRes, err := mod.Execute(ctx, callCtx, "get_entity_profile", map[string]interface{}{
		"topic":       "house",
		"entity_name": "Villa",
	})
	require.NoError(t, err)
	assert.Contains(t, missingRes, "not_found")
}

func TestToolModule_SystemPromptSection(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	mod := memory.NewModule(repo)

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "user-a",
			FamilyGroup: "SmithFamily",
		},
	}

	// Initially empty
	prompt := mod.SystemPromptSection(ctx, callCtx)
	assert.Empty(t, prompt)

	// Add an entity profile
	p := models.EntityProfile{
		FamilyGroup: "SmithFamily",
		Topic:       "baby",
		EntityType:  "baby",
		EntityName:  "Liam",
	}
	p.SetAttributes(map[string]interface{}{
		"birth_date": "2024-05-10",
		"weight_kg":  7.5,
	})
	err := repo.UpsertEntityProfile(ctx, &p)
	require.NoError(t, err)

	// Add pinned memory
	mem := models.TopicMemory{
		UserID:      "user-a",
		FamilyGroup: "SmithFamily",
		Topic:       "baby",
		Subtopic:    "allergies",
		Fact:        "Allergic to dairy drops",
		Scope:       "family",
		IsPinned:    true,
	}
	err = repo.SaveTopicMemory(ctx, &mem)
	require.NoError(t, err)

	prompt = mod.SystemPromptSection(ctx, callCtx)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "Active Entity Profiles & Grounding")
	assert.Contains(t, prompt, "**Liam** (baby • baby)")
	assert.Contains(t, prompt, "birth_date: 2024-05-10")
	assert.Contains(t, prompt, "weight_kg: 7.5")
	assert.Contains(t, prompt, "Pinned Knowledge & Rules")
	assert.Contains(t, prompt, "[baby • allergies] Allergic to dairy drops")
}

func TestConsolidation_DeduplicationAndSuperseding(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()

	// 1. Create duplicate memories
	m1 := models.TopicMemory{
		UserID:      "user-1",
		FamilyGroup: "fam-1",
		Topic:       "health",
		Subtopic:    "routine",
		Fact:        "Take Vitamin D in the morning",
		Scope:       "personal",
		CreatedAt:   time.Now().Add(-2 * time.Hour),
	}
	m2 := models.TopicMemory{
		UserID:      "user-1",
		FamilyGroup: "fam-1",
		Topic:       "health",
		Subtopic:    "routine",
		Fact:        "Take vitamin d in the morning", // exact same normalized
		Scope:       "personal",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, repo.SaveTopicMemory(ctx, &m1))
	require.NoError(t, repo.SaveTopicMemory(ctx, &m2))

	// 2. Create single-value subtopic updates (rent)
	rentOld := models.TopicMemory{
		UserID:      "user-1",
		FamilyGroup: "fam-1",
		Topic:       "house",
		Subtopic:    "rent",
		Fact:        "Monthly rent is $2000",
		Scope:       "family",
		CreatedAt:   time.Now().Add(-5 * time.Hour),
	}
	rentNew := models.TopicMemory{
		UserID:      "user-1",
		FamilyGroup: "fam-1",
		Topic:       "house",
		Subtopic:    "rent",
		Fact:        "Monthly rent is $2200",
		Scope:       "family",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, repo.SaveTopicMemory(ctx, &rentOld))
	require.NoError(t, repo.SaveTopicMemory(ctx, &rentNew))

	// 3. Create structured attribute memories for consolidation aggregation
	babyWeight := models.TopicMemory{
		UserID:      "user-1",
		FamilyGroup: "fam-1",
		Topic:       "baby",
		Subtopic:    "current_weight",
		Fact:        "Liam's weight is 7.4kg",
		Scope:       "family",
		CreatedAt:   time.Now().Add(-30 * time.Minute),
	}
	require.NoError(t, repo.SaveTopicMemory(ctx, &babyWeight))

	// Run consolidation
	res, err := memory.ConsolidateMemories(ctx, repo)
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, 5, res.MemoriesScanned)
	assert.GreaterOrEqual(t, res.MemoriesDeduplicated, 1)
	assert.GreaterOrEqual(t, res.MemoriesSuperseded, 2)
	assert.GreaterOrEqual(t, res.ProfilesUpdated, 1)
	assert.NotEmpty(t, res.Details)

	// Verify older memories are superseded
	active, err := repo.SearchTopicMemories(ctx, "user-1", "fam-1", "health", "", nil)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, m2.ID, active[0].ID)

	activeRent, err := repo.SearchTopicMemories(ctx, "user-1", "fam-1", "house", "", nil)
	require.NoError(t, err)
	require.Len(t, activeRent, 1)
	assert.Equal(t, rentNew.ID, activeRent[0].ID)

	// Verify EntityProfile was aggregated
	prof, err := repo.GetEntityProfile(ctx, "fam-1", "baby", "Liam")
	require.NoError(t, err)
	require.NotNil(t, prof)
	attrs := prof.Attributes()
	assert.Contains(t, attrs, "current_weight")
}

func TestIntegration_RegistryWired(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	mod := memory.NewModule(repo)

	reg := orchestrator.NewRegistry()
	reg.Register(mod)

	found, ok := reg.GetModule("memory")
	assert.True(t, ok)
	assert.Equal(t, "memory", found.Name())

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "u-99",
			FamilyGroup: "Group1",
		},
	}

	result, handled, err := reg.Execute(ctx, callCtx, "save_topic_memory", map[string]interface{}{
		"topic": "finance",
		"fact":  "Primary checking account at Chase",
		"scope": "personal",
	})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Contains(t, result, `"status":"saved"`)
}

type testLLMClient struct {
	orchestrator.LLMClient
}

func (m *testLLMClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

type testDBClient struct {
	orchestrator.DBClient
}

func TestRememberFact_Execution(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	mod := memory.NewModule(repo)

	dbMock := &testDBClient{}
	llmMock := &testLLMClient{}
	bus := events.NewEventBus()

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_123",
			FamilyGroup: "FamAlpha",
		},
		DB:       dbMock,
		LLM:      llmMock,
		EventBus: bus,
	}

	res, err := mod.Execute(ctx, callCtx, "remember_fact", map[string]interface{}{
		"fact":  "Likes dark chocolate 85%",
		"scope": "family",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Fact saved to family memory successfully")

	// P2-4: remember_fact writes exactly one TopicMemory (the single memory store);
	// a family-scoped fact is filed under topic "family".
	mems, err := repo.SearchTopicMemories(ctx, "tg_123", "FamAlpha", models.TopicFamily, "chocolate", nil)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.Equal(t, "Likes dark chocolate 85%", mems[0].Fact)
	assert.Equal(t, "tg_123", mems[0].UserID)
	assert.Equal(t, "FamAlpha", mems[0].FamilyGroup)
	assert.Equal(t, "family", mems[0].Scope)
	all, err := repo.SearchTopicMemories(ctx, "tg_123", "FamAlpha", "", "", nil)
	require.NoError(t, err)
	assert.Len(t, all, 1, "no duplicate write")
}

