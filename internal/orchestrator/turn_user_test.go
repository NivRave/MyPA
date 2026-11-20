package orchestrator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// P2-7: the user is resolved once per turn, not once per tool call.
func TestProcessMessage_ResolvesUserOncePerTurn(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()
	llmMock, ok := engine.llm.(*mocks.MockLLMClient)
	require.True(t, ok)

	var lookups int64
	dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), "user-once").DoAndReturn(func(context.Context, string, string) (*models.User, error) {
		atomic.AddInt64(&lookups, 1)
		return inHousehold(&models.User{PlatformID: "user-once", Name: "Once", FamilyGroup: "fam"}), nil
	}).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	var mu sync.Mutex
	var seenFamily []string
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "schedule",
		declarations: []*genai.FunctionDeclaration{{Name: "t1"}, {Name: "t2"}, {Name: "t3"}},
		executeFunc: func(_ context.Context, callCtx CallContext, _ string, _ map[string]interface{}) (string, error) {
			mu.Lock()
			seenFamily = append(seenFamily, callCtx.Scope.LegacyLabel())
			mu.Unlock()
			return "ok", nil
		},
	})
	engine.SetToolRegistry(reg)

	var calls int64
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string, []models.ChatMessage, string, []byte, string) (*llm.Response, error) {
			if atomic.AddInt64(&calls, 1) == 1 {
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "t1"}, {Name: "t2"}, {Name: "t3"}}}, nil
			}
			return &llm.Response{Text: "done"}, nil
		}).AnyTimes()

	require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{
		MessageID:    "m-once",
		Sender:       "user-once",
		Conversation: channel.Conversation{ID: "c"},
		Text:         "do three things",
		Channel:      "telegram",
	}))
	assert.Equal(t, int64(1), atomic.LoadInt64(&lookups), "GetUser must be called once per turn, not per tool")
	assert.Equal(t, []string{"fam", "fam", "fam"}, seenFamily, "every tool still gets the caller's identity")
}

func TestExecuteSingleTool_UsesTurnUserWithoutLookup(t *testing.T) {
	engine, _, _, cleanup := setupUnauthTestEngine(t)
	defer cleanup()
	// No GetUser expectation: any lookup fails the test.

	var got CallContext
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "probe",
		declarations: []*genai.FunctionDeclaration{{Name: "probe_tool"}},
		executeFunc: func(_ context.Context, callCtx CallContext, _ string, _ map[string]interface{}) (string, error) {
			got = callCtx
			return "ok", nil
		},
	})
	engine.SetToolRegistry(reg)

	user := inHousehold(&models.User{PlatformID: "u-turn", FamilyGroup: "fam-turn", Role: "admin"})
	ctx := withTurnUser(context.Background(), user)
	res := engine.executeSingleTool(ctx, channel.Inbound{Sender: "u-turn"}, nil, "", &genai.FunctionCall{Name: "probe_tool"})
	require.Equal(t, "ok", res)
	assert.Equal(t, "fam-turn", got.Scope.LegacyLabel())
	assert.Equal(t, "admin", got.User.Role)
}

// Callers outside a turn (tests, direct invocations) still get the user via a lookup.
func TestExecuteSingleTool_FallsBackToLookupWithoutTurnUser(t *testing.T) {
	engine, _, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()
	dbMock.EXPECT().GetUser("u-direct").Return(inHousehold(&models.User{PlatformID: "u-direct", FamilyGroup: "fam-direct"}), nil).Times(1)

	var got CallContext
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "probe",
		declarations: []*genai.FunctionDeclaration{{Name: "probe_tool"}},
		executeFunc: func(_ context.Context, callCtx CallContext, _ string, _ map[string]interface{}) (string, error) {
			got = callCtx
			return "ok", nil
		},
	})
	engine.SetToolRegistry(reg)

	engine.executeSingleTool(context.Background(), channel.Inbound{Sender: "u-direct"}, nil, "", &genai.FunctionCall{Name: "probe_tool"})
	assert.Equal(t, "fam-direct", got.Scope.LegacyLabel())
}
