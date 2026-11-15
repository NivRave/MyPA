package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/netguard"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

type testToolModule struct {
	name  string
	decls []*genai.FunctionDeclaration
	exec  func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error)
}

func (m *testToolModule) Name() string { return m.name }
func (m *testToolModule) Declarations() []*genai.FunctionDeclaration { return m.decls }
func (m *testToolModule) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
	if m.exec != nil {
		return m.exec(ctx, callCtx, toolName, args)
	}
	return "", nil
}
func (m *testToolModule) SystemPromptSection(ctx context.Context, callCtx CallContext) string { return "" }

func TestEngine_HandleToolCall_RememberFact(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	var saved models.TopicMemory
	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "memory",
		decls: []*genai.FunctionDeclaration{{Name: "remember_fact"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			fact, _ := args["fact"].(string)
			emb, _ := callCtx.LLM.GenerateEmbedding(ctx, fact)
			saved = models.TopicMemory{UserID: callCtx.ActorID(), Fact: fact, Embedding: pgvector.NewVector(emb)}
			return "Saved fact!", nil
		},
	})
	engine.SetToolRegistry(reg)

	msg := models.Message{UserID: "user1", Text: "remember this"}
	toolCall := &genai.FunctionCall{
		Name: "remember_fact",
		Args: map[string]any{"fact": "I like cats"},
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), "I like cats").Return([]float32{0.1, 0.2}, nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Saved fact!"}, nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Saved fact!", res)
	assert.Equal(t, "user1", saved.UserID)
	assert.Equal(t, "I like cats", saved.Fact)
	assert.Equal(t, pgvector.NewVector([]float32{0.1, 0.2}), saved.Embedding)
}

func TestEngine_HandleToolCall_ReadEmail(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "gmail",
		decls: []*genai.FunctionDeclaration{{Name: "read_email"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			msgID, _ := args["message_id"].(string)
			return engine.gmailClient.ReadEmail(ctx, callCtx.ActorID(), msgID)
		},
	})
	engine.SetToolRegistry(reg)

	toolCall := &genai.FunctionCall{
		Name: "read_email",
		Args: map[string]any{"message_id": "msg-123"},
	}
	gmailMock.EXPECT().ReadEmail(gomock.Any(), "user1", "msg-123").Return("Hello world", nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Email read."}, nil)

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

	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "gmail",
		decls: []*genai.FunctionDeclaration{{Name: "draft_email_reply"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			msgID, _ := args["message_id"].(string)
			replyText, _ := args["reply_text"].(string)
			err := engine.gmailClient.DraftReply(ctx, callCtx.ActorID(), msgID, replyText)
			return "Draft created.", err
		},
	})
	engine.SetToolRegistry(reg)

	toolCall := &genai.FunctionCall{
		Name: "draft_email_reply",
		Args: map[string]any{"message_id": "msg-123", "reply_text": "Hi"},
	}
	gmailMock.EXPECT().DraftReply(gomock.Any(), "user1", "msg-123", "Hi").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Draft created."}, nil)

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

	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "gmail",
		decls: []*genai.FunctionDeclaration{{Name: "archive_emails"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			rawIDs, _ := args["message_ids"].([]interface{})
			for _, raw := range rawIDs {
				_ = engine.gmailClient.ArchiveEmail(ctx, callCtx.ActorID(), raw.(string))
			}
			return "Archived.", nil
		},
	})
	engine.SetToolRegistry(reg)

	toolCall := &genai.FunctionCall{
		Name: "archive_emails",
		Args: map[string]any{"message_ids": []interface{}{"msg-1", "msg-2"}},
	}
	gmailMock.EXPECT().ArchiveEmail(gomock.Any(), "user1", "msg-1").Return(nil)
	gmailMock.EXPECT().ArchiveEmail(gomock.Any(), "user1", "msg-2").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Archived."}, nil)

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

	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "gmail",
		decls: []*genai.FunctionDeclaration{{Name: "soft_delete_emails"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			rawIDs, _ := args["message_ids"].([]interface{})
			for _, raw := range rawIDs {
				_ = engine.gmailClient.SoftDeleteEmail(ctx, callCtx.ActorID(), raw.(string))
			}
			return "Deleted.", nil
		},
	})
	engine.SetToolRegistry(reg)

	toolCall := &genai.FunctionCall{
		Name: "soft_delete_emails",
		Args: map[string]any{"message_ids": []interface{}{"msg-1", "msg-2"}},
	}
	gmailMock.EXPECT().SoftDeleteEmail(gomock.Any(), "user1", "msg-1").Return(nil)
	gmailMock.EXPECT().SoftDeleteEmail(gomock.Any(), "user1", "msg-2").Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Deleted."}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Deleted.", res)
}

func TestEngine_HandleToolCall_FetchWebpage(t *testing.T) {
	netguard.SetAllowLoopbackForTesting(true)
	defer netguard.SetAllowLoopbackForTesting(false)

	engine, tgMock, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>Some webpage text</p></article></body></html>`))
	}))
	defer ts.Close()

	reg := NewRegistry()
	reg.Register(&testToolModule{
		name:  "web_research",
		decls: []*genai.FunctionDeclaration{{Name: "fetch_webpage"}},
		exec: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			_ = callCtx.SendNotification(ctx, callCtx.ActorID(), "📖 Reading webpage...")
			resp, _ := callCtx.LLM.Chat(ctx, "", nil, "read", nil, "")
			return resp.Text, nil
		},
	})
	engine.SetToolRegistry(reg)

	toolCall := &genai.FunctionCall{
		Name: "fetch_webpage",
		Args: map[string]any{"url": ts.URL},
	}
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Read webpage"}, nil)

	res, err := engine.handleToolCall(context.Background(), models.Message{UserID: "user1"}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Read webpage", res)
}
