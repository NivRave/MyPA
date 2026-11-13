package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryV2Repo_GetDeletePinList(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := client.MemoryV2()

	vec := pgvector.NewVector(make([]float32, 3072))

	// 1. Create initial test memories
	mem1 := models.TopicMemory{
		UserID:      "usr-1",
		FamilyGroup: "fam-alpha",
		Topic:       "health",
		Subtopic:    "allergies",
		Fact:        "Peanut allergy detected",
		Scope:       "family",
		IsPinned:    false,
		Embedding:   vec,
		CreatedAt:   time.Now(),
	}
	err := repo.SaveTopicMemory(ctx, &mem1)
	require.NoError(t, err)
	require.NotZero(t, mem1.ID)

	mem2 := models.TopicMemory{
		UserID:      "usr-2",
		FamilyGroup: "fam-beta",
		Topic:       "health",
		Subtopic:    "vaccines",
		Fact:        "Flu shot taken",
		Scope:       "personal",
		IsPinned:    false,
		Embedding:   vec,
		CreatedAt:   time.Now(),
	}

	err = repo.SaveTopicMemory(ctx, &mem2)
	require.NoError(t, err)
	require.NotZero(t, mem2.ID)

	// 2. Test GetTopicMemory
	got1, err := repo.GetTopicMemory(ctx, mem1.ID, "fam-alpha")
	require.NoError(t, err)
	require.NotNil(t, got1)
	assert.Equal(t, "Peanut allergy detected", got1.Fact)

	// Wrong familyGroup returns nil (not found)
	gotWrongFG, err := repo.GetTopicMemory(ctx, mem1.ID, "fam-beta")
	require.NoError(t, err)
	assert.Nil(t, gotWrongFG)

	// Non-existent ID returns nil
	gotNonExistent, err := repo.GetTopicMemory(ctx, 999999)
	require.NoError(t, err)
	assert.Nil(t, gotNonExistent)

	// Direct helper on Client
	clientGot, err := client.GetTopicMemory(ctx, mem1.ID)
	require.NoError(t, err)
	require.NotNil(t, clientGot)
	assert.Equal(t, mem1.ID, clientGot.ID)

	// 3. Test PinTopicMemory
	pinned, err := repo.PinTopicMemory(ctx, mem1.ID, true, "fam-alpha")
	require.NoError(t, err)
	require.NotNil(t, pinned)
	assert.True(t, pinned.IsPinned)

	// Verify persistence
	reCheck, err := repo.GetTopicMemory(ctx, mem1.ID)
	require.NoError(t, err)
	require.NotNil(t, reCheck)
	assert.True(t, reCheck.IsPinned)

	// Pin non-existent
	_, err = repo.PinTopicMemory(ctx, 999999, true)
	require.Error(t, err)

	// 4. Test ListTopicMemories
	listAlpha, err := repo.ListTopicMemories(ctx, "fam-alpha", "health", false)
	require.NoError(t, err)
	require.Len(t, listAlpha, 1)
	assert.Equal(t, mem1.ID, listAlpha[0].ID)

	listBeta, err := repo.ListTopicMemories(ctx, "fam-beta", "", false)
	require.NoError(t, err)
	require.Len(t, listBeta, 1)
	assert.Equal(t, mem2.ID, listBeta[0].ID)

	// Test Supersede & includeSuperseded
	mem3 := models.TopicMemory{
		UserID:      "usr-1",
		FamilyGroup: "fam-alpha",
		Topic:       "health",
		Subtopic:    "allergies",
		Fact:        "Severe peanut allergy detected",
		Scope:       "family",
		Embedding:   vec,
		CreatedAt:   time.Now(),
	}
	err = repo.SaveTopicMemory(ctx, &mem3)

	require.NoError(t, err)

	err = repo.SupersedeMemory(ctx, mem1.ID, mem3.ID)
	require.NoError(t, err)

	withoutSuperseded, err := repo.ListTopicMemories(ctx, "fam-alpha", "health", false)
	require.NoError(t, err)
	require.Len(t, withoutSuperseded, 1)
	assert.Equal(t, mem3.ID, withoutSuperseded[0].ID)

	withSuperseded, err := repo.ListTopicMemories(ctx, "fam-alpha", "health", true)
	require.NoError(t, err)
	require.Len(t, withSuperseded, 2)

	// 5. Test DeleteTopicMemory
	err = repo.DeleteTopicMemory(ctx, mem3.ID, "fam-alpha")
	require.NoError(t, err)

	deletedCheck, err := repo.GetTopicMemory(ctx, mem3.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedCheck)

	// Delete non-existent returns error
	err = repo.DeleteTopicMemory(ctx, 999999)
	require.Error(t, err)
}

func TestMemoryV2Repo_EntityProfilesAndSearch(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := client.MemoryV2()

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Upsert EntityProfile for Family A
	profileA := models.EntityProfile{
		UserID:          userA,
		FamilyGroup:     famA,
		Topic:           models.TopicBaby,
		EntityType:      models.EntityTypeBaby,
		EntityName:      "Emma",
		AttributesJSON:  `{"favorite_toy":"teddy bear"}`,
		UpdatedByUserID: userA,
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.UpsertEntityProfile(ctx, &profileA))
	require.NotZero(t, profileA.ID)

	// 2. Upsert EntityProfile for Family B
	profileB := models.EntityProfile{
		UserID:          userB,
		FamilyGroup:     famB,
		Topic:           models.TopicBaby,
		EntityType:      models.EntityTypeBaby,
		EntityName:      "Lucas",
		AttributesJSON:  `{"favorite_toy":"train set"}`,
		UpdatedByUserID: userB,
		UpdatedAt:       time.Now(),
	}
	require.NoError(t, repo.UpsertEntityProfile(ctx, &profileB))
	require.NotZero(t, profileB.ID)

	// 3. Positive read and cross-tenant isolation
	gotA, err := repo.GetEntityProfile(ctx, famA, models.TopicBaby, "Emma")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Emma", gotA.EntityName)
	assert.Contains(t, gotA.AttributesJSON, "teddy bear")

	// Cross-tenant lookup: Family A querying Lucas returns nil
	gotLeak, err := repo.GetEntityProfile(ctx, famA, models.TopicBaby, "Lucas")
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Family A must not access Family B's entity profile")

	// 4. ListEntityProfiles isolation
	listA, err := repo.ListEntityProfiles(ctx, famA, models.TopicBaby)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, "Emma", listA[0].EntityName)

	listB, err := repo.ListEntityProfiles(ctx, famB, models.TopicBaby)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, "Lucas", listB[0].EntityName)

	// 5. SearchTopicMemories
	vec := pgvector.NewVector(make([]float32, 3072))
	mem := models.TopicMemory{
		UserID:      userA,
		FamilyGroup: famA,
		Topic:       models.TopicBaby,
		Subtopic:    "milestone",
		Fact:        "Emma took her first steps today",
		Scope:       "family",
		TagsJSON:    `["steps","walking"]`,
		Embedding:   vec,
		ValidFrom:   time.Now(),
		CreatedAt:   time.Now(),
	}
	require.NoError(t, repo.SaveTopicMemory(ctx, &mem))

	searchResults, err := repo.SearchTopicMemories(ctx, userA, famA, models.TopicBaby, "first steps", nil)
	require.NoError(t, err)
	require.Len(t, searchResults, 1)
	assert.Equal(t, mem.ID, searchResults[0].ID)

	// Isolation: userB from famB cannot find famA memory
	searchLeak, err := repo.SearchTopicMemories(ctx, userB, famB, models.TopicBaby, "first steps", nil)
	require.NoError(t, err)
	assert.Empty(t, searchLeak, "Family B should not find Family A memories")

	// 6. ListActiveMemoriesForConsolidation
	activeMems, err := repo.ListActiveMemoriesForConsolidation(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, activeMems)
}

