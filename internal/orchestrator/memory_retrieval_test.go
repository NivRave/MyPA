package orchestrator

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// P2-4: per-message retrieval reads the single (v2) memory store, scoped to the caller.
func TestProcessMessage_InjectsTopicMemoriesIntoPrompt(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), "what should I cook?").Return([]float32{0.1}, nil)
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), "user-mem", "", gomock.Any(), 5).Return([]models.TopicMemory{
		{Fact: "Allergic to peanuts", Scope: "personal"},
		{Fact: "Family dinner is on Fridays", Scope: "family"},
	}, nil)

	var systemPrompt string
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sp string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			systemPrompt = sp
			return &llm.Response{Text: "Pasta!"}, nil
		})
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, engine.ProcessMessage(context.Background(), models.Message{
		UserID: "user-mem", ChatID: "c", Text: "what should I cook?", Source: "telegram",
	}))
	assert.Contains(t, systemPrompt, "- Allergic to peanuts")
	assert.Contains(t, systemPrompt, "- Family dinner is on Fridays (Family Shared)")
}
