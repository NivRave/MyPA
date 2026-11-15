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

func TestIsTrivialMessage(t *testing.T) {
	assert.Equal(t, 3, maxTrivialTokens, "threshold is a documented constant")

	trivial := []string{
		"", "   ", "ok", "OK!", "thanks", "Thanks!!", "thank you", "thx 🙏", "👍", "🙏🙏",
		"yes", "no", "sure", "cool", "got it", "ok thanks!", "great, thanks",
		"תודה", "תודה רבה", "כן", "לא", "סבבה", "אוקיי", "יופי תודה", "מעולה!",
	}
	for _, s := range trivial {
		assert.True(t, isTrivialMessage(s), "%q should be trivial", s)
	}

	substantive := []string{
		"remind me tomorrow",          // 3 tokens, but content words
		"yes, delete the second one",  // starts with filler, has content
		"thanks, also book the dentist",
		"no meat tonight",
		"what's on my calendar",
		"תזכיר לי מחר",
		"כן, תמחק את הראשון",
		"ok ok ok thanks thanks", // all filler but more than 3 tokens: not trivially classified
	}
	for _, s := range substantive {
		assert.False(t, isTrivialMessage(s), "%q must not be trivial", s)
	}
}

// Trivial acknowledgements must not cost an embedding call or a vector search.
func TestProcessMessage_TrivialMessageSkipsEmbeddingAndRetrieval(t *testing.T) {
	engine, tgMock, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	// No GenerateEmbedding / SearchSimilarMemories expectations: gomock fails the test if called.
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "You're welcome!"}, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, engine.ProcessMessage(context.Background(), models.Message{
		UserID: "user-triv", ChatID: "c", Text: "thanks! 🙏", Source: "telegram",
	}))
}

func TestProcessMessage_SubstantiveMessageStillRetrievesMemories(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), "remind me tomorrow").Return([]float32{0.1}, nil).Times(1)
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "Sure"}, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, engine.ProcessMessage(context.Background(), models.Message{
		UserID: "user-sub", ChatID: "c", Text: "remind me tomorrow", Source: "telegram",
	}))
}
