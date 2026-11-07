package db

import (
	"context"
	"testing"
	"time"

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
