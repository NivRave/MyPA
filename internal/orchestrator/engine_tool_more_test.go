package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

func TestEngine_HandleToolCall_RememberFact(t *testing.T) {
	engine, _, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	msg := models.Message{UserID: "user1", Text: "remember this"}
	toolCall := &genai.FunctionCall{
		Name: "remember_fact",
		Args: map[string]any{"fact": "I like cats"},
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), "I like cats").Return([]float32{0.1, 0.2}, nil)
	dbMock.EXPECT().SaveMemory(gomock.Any()).DoAndReturn(func(mem models.Memory) error {
		assert.Equal(t, "user1", mem.UserID)
		assert.Equal(t, "I like cats", mem.Fact)
		assert.Equal(t, pgvector.NewVector([]float32{0.1, 0.2}), mem.Embedding)
		return nil
	})
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Saved fact!"}, nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Saved fact!", res)
}

func TestEngine_HandleToolCall_ReadEmail(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	toolCall := &genai.FunctionCall{
		Name: "read_email",
		Args: map[string]any{"message_id": "msg-123"},
	}
	gmailMock.EXPECT().ReadEmail(gomock.Any(), "user1", "msg-123").Return("Hello world", nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Email read."}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Email read.", res)
}

func TestEngine_HandleToolCall_DraftReply(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	toolCall := &genai.FunctionCall{
		Name: "draft_email_reply",
		Args: map[string]any{"message_id": "msg-123", "reply_text": "Hi"},
	}
	gmailMock.EXPECT().DraftReply(gomock.Any(), "user1", "msg-123", "Hi").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Draft created."}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Draft created.", res)
}

func TestEngine_HandleToolCall_ArchiveEmail(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	toolCall := &genai.FunctionCall{
		Name: "archive_emails",
		Args: map[string]any{"message_ids": []interface{}{"msg-1", "msg-2"}},
	}
	gmailMock.EXPECT().ArchiveEmail(gomock.Any(), "user1", "msg-1").Return(nil)
	gmailMock.EXPECT().ArchiveEmail(gomock.Any(), "user1", "msg-2").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Archived."}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Archived.", res)
}

func TestEngine_HandleToolCall_SoftDeleteEmail(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	toolCall := &genai.FunctionCall{
		Name: "soft_delete_emails",
		Args: map[string]any{"message_ids": []interface{}{"msg-1", "msg-2"}},
	}
	gmailMock.EXPECT().SoftDeleteEmail(gomock.Any(), "user1", "msg-1").Return(nil)
	gmailMock.EXPECT().SoftDeleteEmail(gomock.Any(), "user1", "msg-2").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Deleted."}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Deleted.", res)
}

func TestEngine_HandleToolCall_FetchWebpage(t *testing.T) {
	engine, tgMock, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	// Need to mock fetch_webpage. Wait, fetch_webpage calls scraper.FetchAndExtractText which takes a real URL.
	// We could use an httptest.Server to serve mock HTML!
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><article><p>Some webpage text</p></article></body></html>`))
	}))
	defer ts.Close()

	toolCall := &genai.FunctionCall{
		Name: "fetch_webpage",
		Args: map[string]any{"url": ts.URL},
	}
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Read webpage"}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Read webpage", res)
}
