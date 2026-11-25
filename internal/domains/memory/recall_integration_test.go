package memory_test

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// topicEmbedder returns a different direction in embedding space per subject, so that "similar"
// has a definite meaning in the test: texts about the same subject get the same vector.
type topicEmbedder struct {
	orchestrator.LLMClient
}

func (topicEmbedder) GenerateEmbedding(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 3072)
	switch lower := strings.ToLower(text); {
	case strings.Contains(lower, "peanut"):
		v[10] = 1
	case strings.Contains(lower, "boiler"):
		v[20] = 1
	default:
		v[30] = 1
	}
	return v, nil
}

// V4-06, against real Postgres and pgvector: a fact saved with save_topic_memory is what the
// turn's memory retrieval returns for a question about the same subject. Before the change only
// remember_fact stored an embedding, so facts saved this way were never found by meaning.
func TestSaveTopicMemory_IsRecalledByMeaning(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := db.NewMemoryV2Repo(client.DB)
	mod := memory.NewModule(repo)
	llm := topicEmbedder{}
	scope := householdtest.Scope(t, client, "recall_family", "recall_user", "family")
	callCtx := orchestrator.CallContext{
		User:     &models.User{PlatformID: "recall_user", FamilyGroup: "recall_family"},
		Scope:    scope,
		LLM:      llm,
		EventBus: events.NewEventBus(),
	}

	// Memories as they were written before V4-06: no embedding (a zero vector).
	for i := 0; i < 6; i++ {
		require.NoError(t, repo.SaveTopicMemory(ctx, scope, &models.TopicMemory{
			UserID: "recall_user", FamilyGroup: "recall_family", Topic: "personal", TagsJSON: "[]", Scope: "personal",
			Fact: fmt.Sprintf("An older note without an embedding, number %d", i), CreatedByUserID: "recall_user", ValidFrom: time.Now(),
		}))
	}

	for _, args := range []map[string]interface{}{
		{"topic": "house", "fact": "The boiler is serviced every November.", "scope": "family"},
		{"topic": "health", "fact": "Dana is allergic to peanuts.", "scope": "personal"},
	} {
		res, err := mod.Execute(ctx, callCtx, "save_topic_memory", args)
		require.NoError(t, err)
		require.Contains(t, res, `"status":"saved"`)
	}

	recall := func(question string) []models.TopicMemory {
		t.Helper()
		embedding, err := llm.GenerateEmbedding(ctx, question)
		require.NoError(t, err)
		got, err := repo.SearchSimilarMemories(ctx, scope, pgvector.NewVector(embedding), 1)
		require.NoError(t, err)
		return got
	}

	got := recall("Can I bring peanut butter cookies?")
	require.Len(t, got, 1)
	assert.Equal(t, "Dana is allergic to peanuts.", got[0].Fact)

	got = recall("When is the boiler due?")
	require.Len(t, got, 1)
	assert.Equal(t, "The boiler is serviced every November.", got[0].Fact)
}

// BL-26: save_topic_memory normalises scope as the removed remember_fact did (trim, lower-case,
// "family" or else "personal"). A save with "Family" is stored as family and found by another
// member of the family; any other value is personal.
func TestSaveTopicMemory_ScopeIsNormalised(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := db.NewMemoryV2Repo(client.DB)
	mod := memory.NewModule(repo)
	llm := topicEmbedder{}
	dana := householdtest.Scope(t, client, "scope_family", "scope_dana", "family")
	omer := householdtest.Scope(t, client, "scope_family", "scope_omer", "family")
	callCtx := orchestrator.CallContext{
		User:     &models.User{PlatformID: "scope_dana", FamilyGroup: "scope_family"},
		Scope:    dana,
		LLM:      llm,
		EventBus: events.NewEventBus(),
	}

	for _, args := range []map[string]interface{}{
		{"topic": "house", "fact": "The boiler is serviced every November.", "scope": "Family"},
		{"topic": "health", "fact": "Dana is allergic to peanuts.", "scope": " Shared "},
	} {
		res, err := mod.Execute(ctx, callCtx, "save_topic_memory", args)
		require.NoError(t, err)
		require.Contains(t, res, `"status":"saved"`)
	}
	var stored []models.TopicMemory
	require.NoError(t, client.DB.Order("fact").Find(&stored).Error)
	require.Len(t, stored, 2)
	assert.Equal(t, "personal", stored[0].Scope, "an unknown value is personal")
	assert.Equal(t, "family", stored[1].Scope, `"Family" is stored as family`)

	boiler, err := llm.GenerateEmbedding(ctx, "When is the boiler due?")
	require.NoError(t, err)
	got, err := repo.SearchSimilarMemories(ctx, omer, pgvector.NewVector(boiler), 5)
	require.NoError(t, err)
	require.Len(t, got, 1, "another family member finds the family memory")
	assert.Equal(t, "The boiler is serviced every November.", got[0].Fact)
	peanut, err := llm.GenerateEmbedding(ctx, "peanut")
	require.NoError(t, err)
	got, err = repo.SearchSimilarMemories(ctx, omer, pgvector.NewVector(peanut), 5)
	require.NoError(t, err)
	for _, m := range got {
		assert.NotEqual(t, "Dana is allergic to peanuts.", m.Fact, "a personal memory stays private")
	}
}
