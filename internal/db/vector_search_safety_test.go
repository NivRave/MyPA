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

// TestVectorDimensionAndScanSafety stress-tests:
// 1. Saving topic memories without pre-computed embeddings (uninitialized pgvector.Vector) against PostgreSQL (preventing SQLSTATE 22000).
// 2. Saving topic memories with explicit zero-length float slice (pgvector.NewVector([]float32{})).
// 3. Direct GORM insertion (client.DB.Create) triggering BeforeCreate hook.
// 4. Direct GORM update/save (client.DB.Save) triggering BeforeSave hook.
// 5. Reading & scanning via GetTopicMemory and ListTopicMemories without "unsupported data type: <nil>".
// 6. PinTopicMemory toggling and saving without dimension error.
func TestVectorDimensionAndScanSafety(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := client.MemoryV2()
	fg := "TestVectorSafetyFamily"
	s := scopeFor(t, client, fg)

	t.Run("SaveTopicMemory_UninitializedEmbedding_SucceedsWithoutSQLSTATE22000", func(t *testing.T) {
		mem := models.TopicMemory{
			UserID:      "usr-challenger-1",
			FamilyGroup: fg,
			Topic:       "health",
			Subtopic:    "vitals",
			Fact:        "Resting heart rate 62 bpm",
			Scope:       "personal",
			// Embedding field is completely uninitialized (zero value pgvector.Vector)
			CreatedAt: time.Now(),
		}

		err := repo.SaveTopicMemory(ctx, s, &mem)
		require.NoError(t, err, "SaveTopicMemory with uninitialized embedding must succeed without SQLSTATE 22000")
		require.NotZero(t, mem.ID)

		// Verify GetTopicMemory parses and scans cleanly
		scanned, err := repo.GetTopicMemory(ctx, s, mem.ID)
		require.NoError(t, err, "GetTopicMemory must scan cleanly without 'unsupported data type: <nil>'")
		require.NotNil(t, scanned)
		assert.Equal(t, mem.Fact, scanned.Fact)
		assert.Equal(t, 3072, len(scanned.Embedding.Slice()), "Scanned vector must have 3072 dimensions")
	})

	t.Run("SaveTopicMemory_ExplicitZeroLengthSlice_SucceedsWithoutSQLSTATE22000", func(t *testing.T) {
		mem := models.TopicMemory{
			UserID:      "usr-challenger-2",
			FamilyGroup: fg,
			Topic:       "house",
			Subtopic:    "repairs",
			Fact:        "Roof inspection due in May",
			Scope:       "family",
			Embedding:   pgvector.NewVector([]float32{}),
			CreatedAt:   time.Now(),
		}

		err := repo.SaveTopicMemory(ctx, s, &mem)
		require.NoError(t, err, "SaveTopicMemory with empty slice vector must succeed without SQLSTATE 22000")
		require.NotZero(t, mem.ID)

		// Verify GetTopicMemory parses and scans cleanly
		scanned, err := repo.GetTopicMemory(ctx, s, mem.ID)
		require.NoError(t, err, "GetTopicMemory must scan cleanly")
		require.NotNil(t, scanned)
		assert.Equal(t, 3072, len(scanned.Embedding.Slice()))
	})

	t.Run("GORM_DirectCreate_TriggersBeforeCreateHook", func(t *testing.T) {
		mem := models.TopicMemory{
			UserID:      "usr-challenger-3",
			FamilyGroup: fg,
			HouseholdID: s.HouseholdID(),
			Topic:       "finance",
			Subtopic:    "tax",
			Fact:        "W2 submitted",
			Scope:       "personal",
			TagsJSON:    "[]",
			CreatedAt:   time.Now(),
		}

		// Directly insert via GORM without calling repository helper
		err := client.DB.WithContext(ctx).Create(&mem).Error
		require.NoError(t, err, "GORM BeforeCreate hook must populate 3072-dim vector to satisfy PostgreSQL")
		require.NotZero(t, mem.ID)

		// Verify ListTopicMemories scans the direct-inserted row cleanly
		list, err := repo.ListTopicMemories(ctx, s, "finance", false)
		require.NoError(t, err, "ListTopicMemories must scan cleanly without error")
		require.Len(t, list, 1)
		assert.Equal(t, "W2 submitted", list[0].Fact)
		assert.Equal(t, 3072, len(list[0].Embedding.Slice()))
	})

	t.Run("GORM_DirectSave_TriggersBeforeSaveHook", func(t *testing.T) {
		mem := models.TopicMemory{
			UserID:      "usr-challenger-4",
			FamilyGroup: fg,
			Topic:       "baby",
			Subtopic:    "sleep",
			Fact:        "Slept 7 hours",
			Scope:       "family",
			TagsJSON:    "[]",
			CreatedAt:   time.Now(),
		}

		err := repo.SaveTopicMemory(ctx, s, &mem)
		require.NoError(t, err)

		// Manually zero out the in-memory struct vector and call Save
		mem.Embedding = pgvector.Vector{}
		mem.Fact = "Slept 8 hours uninterrupted"
		err = client.DB.WithContext(ctx).Save(&mem).Error
		require.NoError(t, err, "GORM BeforeSave hook must prevent zero-dimension vector on Save")

		reloaded, err := repo.GetTopicMemory(ctx, s, mem.ID)
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		assert.Equal(t, "Slept 8 hours uninterrupted", reloaded.Fact)
		assert.Equal(t, 3072, len(reloaded.Embedding.Slice()))
	})

	t.Run("PinTopicMemory_TogglesAndPersistsWithoutVectorDimensionErrors", func(t *testing.T) {
		mem := models.TopicMemory{
			UserID:      "usr-challenger-5",
			FamilyGroup: fg,
			Topic:       "work",
			Subtopic:    "goals",
			Fact:        "Complete Milestone 1C verification",
			Scope:       "personal",
			CreatedAt:   time.Now(),
		}

		err := repo.SaveTopicMemory(ctx, s, &mem)
		require.NoError(t, err)

		// Pin the memory
		pinned, err := repo.PinTopicMemory(ctx, s, mem.ID, true)
		require.NoError(t, err, "PinTopicMemory must succeed without dimension error")
		require.NotNil(t, pinned)
		assert.True(t, pinned.IsPinned)

		// Verify via ListTopicMemories
		mems, err := repo.ListTopicMemories(ctx, s, "work", false)
		require.NoError(t, err)
		require.Len(t, mems, 1)
		assert.True(t, mems[0].IsPinned)
		assert.Equal(t, 3072, len(mems[0].Embedding.Slice()))
	})

	t.Run("ListTopicMemories_BulkScanningSafety", func(t *testing.T) {
		// Verify that listing all memories across topics scans multiple rows without scan nil errors
		allMems, err := repo.ListTopicMemories(ctx, s, "all", false)
		require.NoError(t, err, "Bulk scanning in ListTopicMemories must not throw unsupported data type: <nil>")
		assert.True(t, len(allMems) >= 5, "Expected at least 5 memories in the test family")
		for _, m := range allMems {
			assert.Equal(t, 3072, len(m.Embedding.Slice()), "Every scanned memory must have valid 3072 dimensions")
		}
	})
}
