package memory_test

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullSizeEmbedder returns a vector of the stored size, as the configured embedding model does.
type fullSizeEmbedder struct {
	orchestrator.LLMClient
}

func (fullSizeEmbedder) GenerateEmbedding(context.Context, string) ([]float32, error) {
	v := make([]float32, models.EmbeddingDimensions)
	for i := range v {
		v[i] = 0.0123456
	}
	return v, nil
}

// BL-25: the undo snapshot of a saved memory holds no vector and stays small. The memory itself
// keeps its embedding.
func TestSaveTopicMemory_SnapshotHoldsNoVector(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	mod := memory.NewModule(repo)
	callCtx := memoryCallCtx(t, fullSizeEmbedder{})
	var got []events.MutationEvent
	callCtx.EventBus.SubscribeSync(func(_ context.Context, ev events.MutationEvent) error {
		got = append(got, ev)
		return nil
	})

	_, err := mod.Execute(ctx, callCtx, "save_topic_memory", map[string]interface{}{
		"topic": "family", "fact": "Likes dark chocolate 85%", "scope": "family",
	})
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.NotContains(t, got[0].AfterJSON, `"embedding"`, "the snapshot carries no vector")
	assert.Less(t, len(got[0].AfterJSON), 1024, "the snapshot stays small")
	assert.Contains(t, got[0].AfterJSON, "Likes dark chocolate 85%")

	mems, err := repo.SearchTopicMemories(ctx, sc(t, "FamAlpha", "tg_123"), models.TopicFamily, "chocolate", nil)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.Len(t, mems[0].Embedding.Slice(), models.EmbeddingDimensions, "the memory keeps its embedding")
}
