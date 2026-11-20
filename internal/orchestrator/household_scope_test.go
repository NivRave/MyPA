package orchestrator

import (
	"context"
	"strings"
	"sync"
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

// V4-30: a sender whose users row has no usable household gets one refusal, and nothing else
// happens — no model call, no tool (the unauth engine's LLM mock has no expectations, so any
// call fails the test).
func TestProcessMessage_SenderWithoutAHouseholdIsRefused(t *testing.T) {
	cases := map[string]string{
		"empty household":             "",
		"household that is not there": "6f1c6f3e-0000-4000-8000-00000000dead",
	}
	for name, hh := range cases {
		t.Run(name, func(t *testing.T) {
			engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
			defer cleanup()
			dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), "orphan").Return(&models.User{PlatformID: "orphan", Role: "admin", HouseholdID: hh}, nil)

			reg := NewRegistry()
			reg.Register(&mockToolModule{
				name:         "probe",
				declarations: []*genai.FunctionDeclaration{{Name: "probe_tool"}},
				executeFunc: func(context.Context, CallContext, string, map[string]interface{}) (string, error) {
					t.Fatal("no tool may run for a sender without a household")
					return "", nil
				},
			})
			engine.SetToolRegistry(reg)

			var sent []string
			tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, text string) error { sent = append(sent, text); return nil }).Times(1)

			require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{
				MessageID: "m-orphan", Sender: "orphan", Conversation: channel.Conversation{ID: "c"}, Text: "/backup", Channel: "telegram",
			}))
			require.Len(t, sent, 1)
			assert.Contains(t, sent[0], "not attached to a household")
		})
	}
}

// promptFor runs one turn for a user of the given household and returns the system prompt.
func promptFor(t *testing.T, h models.Household) string {
	t.Helper()
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()
	llmMock, ok := engine.llm.(*mocks.MockLLMClient)
	require.True(t, ok)
	testHouseholds.Put(h)

	dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), "u-hh").Return(&models.User{PlatformID: "u-hh", Name: "Hila", Role: "family", HouseholdID: h.ID}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	var mu sync.Mutex
	var prompt string
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sp string, _ []models.ChatMessage, _ string, _ []byte, _ string) (*llm.Response, error) {
			mu.Lock()
			prompt = sp
			mu.Unlock()
			return &llm.Response{Text: "ok"}, nil
		})

	require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{
		MessageID: "m-" + h.ID, Sender: "u-hh", Conversation: channel.Conversation{ID: "c"}, Text: "what time is it?", Channel: "telegram",
	}))
	mu.Lock()
	defer mu.Unlock()
	return prompt
}

func household(id, label, tz, lang string) models.Household {
	l := label
	return models.Household{ID: id, Name: label, LegacyLabel: &l, TimeZone: tz, Language: lang}
}

// V4-30: the turn's time zone is the household's; without one it is the install's default
// (the engine's "UTC" in these tests).
func TestProcessMessage_TimeZoneComesFromTheHousehold(t *testing.T) {
	tokyo := promptFor(t, household("7a3b0c1d-0000-4000-8000-000000000001", "Tanaka", "Asia/Tokyo", ""))
	assert.Contains(t, tokyo, "timezone Asia/Tokyo")

	plain := promptFor(t, household("7a3b0c1d-0000-4000-8000-000000000002", "Levi-tz", "", ""))
	assert.Contains(t, plain, "timezone UTC")
	assert.NotContains(t, plain, "Asia/Tokyo")
}

// V4-30: the household's language reaches the prompt only when it is set.
func TestProcessMessage_HouseholdLanguageLineOnlyWhenSet(t *testing.T) {
	hebrew := promptFor(t, household("7a3b0c1d-0000-4000-8000-000000000003", "Cohen-lang", "", "he"))
	assert.Contains(t, hebrew, "The household's language is he")
	assert.Contains(t, hebrew, "Household: Cohen-lang")

	none := promptFor(t, household("7a3b0c1d-0000-4000-8000-000000000004", "Levi-lang", "", ""))
	assert.NotContains(t, none, "household's language")
}

// V4-30: the operator commands go through scope.Can — refused for a family member, allowed for an
// admin of the same household.
func TestOperatorCommands_FollowTheScopesRole(t *testing.T) {
	for _, cmd := range []string{"/backup", "/doctor", "/restore"} {
		for _, role := range []string{"family", "admin"} {
			t.Run(strings.TrimPrefix(cmd, "/")+"_"+role, func(t *testing.T) {
				engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
				defer cleanup()
				t.Setenv("DATABASE_URL", "")
				user := inHousehold(&models.User{PlatformID: "op-" + role, Role: role, FamilyGroup: "Ops"})
				dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), user.PlatformID).Return(user, nil).AnyTimes()
				var sent []string
				tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, _ string, text string) error { sent = append(sent, text); return nil }).AnyTimes()

				require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{
					MessageID: "m-" + cmd + role, Sender: user.PlatformID, Conversation: channel.Conversation{ID: "c"}, Text: cmd, Channel: "telegram",
				}))
				require.NotEmpty(t, sent)
				denied := strings.Contains(strings.Join(sent, "\n"), "Permission denied")
				assert.Equal(t, role == "family", denied, "%s for a %s: denied=%v, replies %q", cmd, role, denied, sent)
			})
		}
	}
}
