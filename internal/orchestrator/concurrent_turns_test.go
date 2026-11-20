package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// P2-8 makes the consumer run different users' turns concurrently on one Engine. This exercises
// that (run with -race in CI/containers): concurrent turns with tool calls must not share state.
func TestProcessMessage_ConcurrentTurnsForDifferentUsers(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	var mu sync.Mutex
	actorsSeen := map[string]string{} // tool arg "for" → ActorID that executed it
	reg := NewRegistry()
	reg.Register(&mockToolModule{
		name:         "schedule",
		declarations: []*genai.FunctionDeclaration{{Name: "create_calendar_event"}},
		executeFunc: func(_ context.Context, callCtx CallContext, _ string, args map[string]interface{}) (string, error) {
			mu.Lock()
			actorsSeen[fmt.Sprint(args["for"])] = callCtx.ActorID()
			mu.Unlock()
			return "ok", nil
		},
	})
	engine.SetToolRegistry(reg)

	// Each user's first LLM call requests a tool tagged with that user's ID; the second returns text.
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, history []models.ChatMessage, prompt string, _ []byte, _ string) (*llm.Response, error) {
			if strings.HasPrefix(prompt, "book for ") {
				return &llm.Response{ToolCalls: []*genai.FunctionCall{{
					Name: "create_calendar_event", Args: map[string]interface{}{"for": strings.TrimPrefix(prompt, "book for ")},
				}}}, nil
			}
			return &llm.Response{Text: "booked"}, nil
		}).AnyTimes()

	const users = 12
	var wg sync.WaitGroup
	for i := 0; i < users; i++ {
		uid := fmt.Sprintf("user-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := engine.ProcessMessage(context.Background(), channel.Inbound{
				MessageID:    "m-" + uid,
				Sender:       uid,
				Conversation: channel.Conversation{ID: "chat-" + uid},
				Text:         "book for " + uid,
				Channel:      "telegram",
			})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, actorsSeen, users)
	for target, actor := range actorsSeen {
		assert.Equal(t, target, actor, "a tool must run under the identity of the turn that requested it")
	}
}
