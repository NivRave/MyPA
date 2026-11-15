package db

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P2-4: legacy `memories` rows are copied into `topic_memories` once, idempotently, without
// duplicating facts that the old remember_fact dual-write already stored there.
func TestMigrateLegacyMemories(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	gdb := client.DB

	require.NoError(t, gdb.Create(&models.User{PlatformID: "u1", Name: "One", FamilyGroup: "fam_x"}).Error)
	require.NoError(t, gdb.Create(&models.User{PlatformID: "u2", Name: "Two", FamilyGroup: "fam_x"}).Error)

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	legacy := []models.Memory{
		{UserID: "u1", Scope: "personal", Fact: "u1 likes oat milk", Embedding: mixVec(0.9, 1), CreatedAt: created},
		{UserID: "u1", Scope: "family", Fact: "family wifi password is on the fridge", Embedding: mixVec(0.8, 2), CreatedAt: created},
		{UserID: "orphan", Scope: "personal", Fact: "orphan fact", Embedding: mixVec(0.1, 3), CreatedAt: created},
		{UserID: "u1", Scope: "personal", Fact: "already dual-written", Embedding: mixVec(0.5, 4), CreatedAt: created},
	}
	for _, m := range legacy {
		require.NoError(t, gdb.Create(&m).Error)
	}
	// The old remember_fact dual-write already stored this one in v2.
	require.NoError(t, NewMemoryV2Repo(gdb).SaveTopicMemory(ctx, &models.TopicMemory{
		UserID: "u1", FamilyGroup: "fam_x", Topic: models.TopicPersonal, Subtopic: "fact", Scope: "personal",
		Fact: "already dual-written", Embedding: mixVec(0.5, 4),
	}))

	n, err := MigrateLegacyMemories(gdb)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "3 legacy facts are new to topic_memories")

	var all []models.TopicMemory
	require.NoError(t, gdb.Order("fact").Find(&all).Error)
	require.Len(t, all, 4, "no duplicate for the dual-written fact")
	byFact := map[string]models.TopicMemory{}
	for _, m := range all {
		byFact[m.Fact] = m
	}

	fam := byFact["family wifi password is on the fridge"]
	assert.Equal(t, "fam_x", fam.FamilyGroup, "family group comes from the users table")
	assert.Equal(t, "family", fam.Scope)
	assert.Equal(t, models.TopicFamily, fam.Topic)
	assert.Equal(t, "u1", fam.CreatedByUserID)
	assert.True(t, fam.CreatedAt.Equal(created), "original timestamp preserved")
	assert.Len(t, fam.Embedding.Slice(), 3072, "embedding preserved")

	assert.Equal(t, models.TopicPersonal, byFact["u1 likes oat milk"].Topic)
	assert.Equal(t, "", byFact["orphan fact"].FamilyGroup, "users without a users row keep an empty family group")

	// Idempotent: a second run (every startup) inserts nothing.
	n, err = MigrateLegacyMemories(gdb)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// The migrated family memory is visible to another family member via the v2 search.
	got, err := client.SearchSimilarMemories(ctx, "u2", "fam_x", mixVec(0.8, 2), 5)
	require.NoError(t, err)
	var facts []string
	for _, m := range got {
		facts = append(facts, m.Fact)
	}
	assert.Contains(t, facts, "family wifi password is on the fridge")
	assert.NotContains(t, facts, "u1 likes oat milk", "u1's personal memory must stay private")
}
