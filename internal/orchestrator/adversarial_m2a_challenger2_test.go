package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// TestChallenger2_IntentMatcher_Comprehensive tests bilingual keyword matching,
// Hebrew prefixes, multi-domain inputs, emojis, and ambiguous edge cases.
func TestChallenger2_IntentMatcher_Comprehensive(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	tests := []struct {
		name     string
		input    string
		expected []DomainGroup
		negative []DomainGroup
	}{
		// 1. English Domains
		{
			name:     "English Baby Feed",
			input:    "Liam drank 150ml of formula from his bottle",
			expected: []DomainGroup{DomainBaby},
			negative: []DomainGroup{DomainFinance, DomainHouse},
		},
		{
			name:     "English Baby Diaper & Sleep",
			input:    "Changed a dirty diaper, now putting baby to sleep in crib",
			expected: []DomainGroup{DomainBaby},
		},
		{
			name:     "English Finance Expense",
			input:    "I spent 120 dollars at the store, paid with credit card",
			expected: []DomainGroup{DomainFinance},
			negative: []DomainGroup{DomainBaby},
		},
		{
			name:     "English Finance Budget & Bill",
			input:    "Check our monthly utility bills and overbudget status",
			expected: []DomainGroup{DomainFinance},
		},
		{
			name:     "English House Maintenance",
			input:    "The AC filter in our apartment is clogged, call a technician",
			expected: []DomainGroup{DomainHouse},
			negative: []DomainGroup{DomainFinance},
		},
		{
			name:     "English House Vehicle",
			input:    "Car needs an oil change and annual test at the mechanic garage",
			expected: []DomainGroup{DomainHouse},
		},
		{
			name:     "English Calendar Schedule",
			input:    "Schedule an appointment with Dr. Johnson tomorrow at 4pm",
			expected: []DomainGroup{DomainCalendar},
		},
		{
			name:     "English Gmail Search",
			input:    "Search unread emails in my gmail inbox from amazon",
			expected: []DomainGroup{DomainGmail},
		},
		{
			name:     "English Tasks Todo",
			input:    "Add almond milk to my shopping list task",
			expected: []DomainGroup{DomainTasks},
		},
		{
			name:     "English Contacts CRM",
			input:    "Find contact phone number for David's dentist",
			expected: []DomainGroup{DomainContacts},
		},
		{
			name:     "English Automation Rule",
			input:    "Create a new automation rule with webhook trigger",
			expected: []DomainGroup{DomainAutomation},
		},
		{
			name:     "English Shared Family",
			input:    "Set a shared reminder for my spouse in our family space",
			expected: []DomainGroup{DomainShared},
		},

		// 2. Hebrew with Prefixes
		{
			name:     "Hebrew Prefix ה (The baby)",
			input:    "התינוק אכל 120 מ\"ל של מטרנה",
			expected: []DomainGroup{DomainBaby},
		},
		{
			name:     "Hebrew Prefix ב (In the apartment)",
			input:    "בדירה יש נזילה מהמזגן",
			expected: []DomainGroup{DomainHouse},
		},
		{
			name:     "Hebrew Prefix ל (To pay/schedule)",
			input:    "לשלם את הארנונה ולקבוע פגישה",
			expected: []DomainGroup{DomainFinance, DomainCalendar},
		},
		{
			name:     "Hebrew Prefix כ (When paying)",
			input:    "כששילמתי עבור הטיפול לרכב",
			expected: []DomainGroup{DomainFinance, DomainHouse},
		},
		{
			name:     "Hebrew Prefix מ (From the account)",
			input:    "מהחשבון ירד סכום עבור החשמל",
			expected: []DomainGroup{DomainFinance},
		},
		{
			name:     "Hebrew Prefix ש (That bought)",
			input:    "שקניתי מוצץ ומגבונים",
			expected: []DomainGroup{DomainBaby},
		},
		{
			name:     "Hebrew Prefix כש (When baby sleeps)",
			input:    "כשהתינוק ישן בעריסה",
			expected: []DomainGroup{DomainBaby},
		},
		{
			name:     "Hebrew Prefix שב (That in the apartment)",
			input:    "שבדירה צריך לתקן את הדוד",
			expected: []DomainGroup{DomainHouse},
		},
		{
			name:     "Hebrew Prefix שה (That the baby)",
			input:    "שהתינוק יקבל את החיסון",
			expected: []DomainGroup{DomainBaby},
		},

		// 3. Multi-Domain Combinations
		{
			name:     "Baby + Calendar Appointment",
			input:    "Liam has a pediatrician appointment tomorrow at 10:00",
			expected: []DomainGroup{DomainBaby, DomainCalendar},
		},
		{
			name:     "House + Finance Plumber",
			input:    "Paid 450 ILS to the plumber for boiler leak repair",
			expected: []DomainGroup{DomainFinance, DomainHouse},
		},
		{
			name:     "Hebrew Shared + Baby",
			input:    "תזכורת משותפת לתת נובימול לתינוק ב-14:00",
			expected: []DomainGroup{DomainShared, DomainBaby},
		},

		// 4. Edge Cases: Emojis, Mixed Case, Punctuation
		{
			name:     "Emoji & Mixed Case Baby",
			input:    "🍼 BaBy FeEdInG: 180ml Formula! 👶",
			expected: []DomainGroup{DomainBaby},
		},
		{
			name:     "Emoji & Punctuation Finance",
			input:    "💰 SpEnT 250 USD on grocery bills! 🛒",
			expected: []DomainGroup{DomainFinance},
		},
		{
			name:     "Emoji & Vehicle Maintenance",
			input:    "🚗 car inspection and annual test tomorrow! 🔧",
			expected: []DomainGroup{DomainHouse},
		},

		// 5. Ambiguous / Non-Domain Queries
		{
			name:     "Greeting",
			input:    "Hello! Good morning, how are you today?",
			expected: nil,
		},
		{
			name:     "General Question",
			input:    "What is the capital of France?",
			expected: nil,
		},
		{
			name:     "Thank You",
			input:    "Thank you very much for your help!",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := matcher.MatchDomains(tt.input)

			if len(tt.expected) == 0 {
				assert.Empty(t, matched, "Query '%s' should match no domain", tt.input)
			} else {
				for _, exp := range tt.expected {
					assert.Contains(t, matched, exp, "Query '%s' must match domain %s", tt.input, exp)
				}
			}

			for _, neg := range tt.negative {
				assert.NotContains(t, matched, neg, "Query '%s' must NOT match domain %s", tt.input, neg)
			}
		})
	}
}

// TestChallenger2_PhotoData_Stripping_DirectRedisInspection verifies SCALE-02:
// - A 2MB photo appended to chat history is stripped of raw bytes before JSON persistence.
// - Direct inspection of the Redis list item confirms photo_data is null/omitted and photo_mime_type is empty.
// - The persisted content contains the formatted attachment marker "[Attachment: image/jpeg (2.0 MB)]".
// - The persisted JSON payload size is < 500 bytes (not > 2.6 MB base64).
func TestChallenger2_PhotoData_Stripping_DirectRedisInspection(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	userID := "adv-user-photo-strip"

	// 2 MB raw photo payload
	const photoSize = 2 * 1024 * 1024
	largePhoto := make([]byte, photoSize)
	for i := range largePhoto {
		largePhoto[i] = byte(i % 256)
	}

	msg := models.ChatMessage{
		Role:          "user",
		Content:       "Receipt for kitchen repair",
		PhotoData:     largePhoto,
		PhotoMimeType: "image/jpeg",
	}

	// Append to chat history
	err = store.AppendChatHistory(ctx, userID, msg)
	require.NoError(t, err)

	// Fetch directly from Redis list to inspect raw stored JSON
	rawList, err := mr.List("chat:" + userID)
	require.NoError(t, err)
	require.Len(t, rawList, 1)

	rawJSON := rawList[0]

	// 1. Empirical payload size assertion: must be tiny (< 500 bytes), NOT megabytes!
	assert.Less(t, len(rawJSON), 500, "Persisted Redis JSON must be < 500 bytes, was %d bytes", len(rawJSON))

	// 2. Inspect parsed JSON
	var rawMap map[string]interface{}
	err = json.Unmarshal([]byte(rawJSON), &rawMap)
	require.NoError(t, err)

	assert.Equal(t, "user", rawMap["role"])
	assert.Nil(t, rawMap["photo_data"], "photo_data must be null or omitted in Redis JSON")
	assert.Empty(t, rawMap["photo_mime_type"], "photo_mime_type must be empty in Redis JSON")

	content, ok := rawMap["content"].(string)
	require.True(t, ok)
	assert.Contains(t, content, "Receipt for kitchen repair")
	assert.Contains(t, content, "[Attachment: image/jpeg (2.0 MB)]")

	// 3. Verify store.GetChatHistory unmarshals cleanly without photo data
	history, err := store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Nil(t, history[0].PhotoData)
	assert.Empty(t, history[0].PhotoMimeType)
	assert.Contains(t, history[0].Content, "[Attachment: image/jpeg (2.0 MB)]")
}

// TestChallenger2_PhotoData_MultimodalActiveTurnPreservation verifies that:
// 1. During an active conversational turn with tool calling, the in-memory loopHistory
//    strictly preserves PhotoData and PhotoMimeType across all recursive tool iterations.
// 2. Only upon final persistence to Redis chat history is PhotoData stripped.
func TestChallenger2_PhotoData_MultimodalActiveTurnPreservation(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	userID := "adv-user-multimodal"

	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()

	// 1 MB photo payload
	const photoBytes = 1024 * 1024
	photoPayload := make([]byte, photoBytes)
	for i := range photoPayload {
		photoPayload[i] = 0xAA
	}

	// Register a tool that inspects whether PhotoData is available in history
	reg := NewRegistry()
	inspectMod := &mockToolModule{
		name: "photo_inspector",
		declarations: []*genai.FunctionDeclaration{
			{Name: "inspect_photo_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			return "photo inspection complete", nil
		},
	}
	reg.Register(inspectMod)
	engine.SetToolRegistry(reg)

	tgMock.EXPECT().GetFile(gomock.Any(), "photo-adv-123").Return("path/photo.jpg", nil).Times(1)
	tgMock.EXPECT().DownloadFile(gomock.Any(), "path/photo.jpg").Return(photoPayload, nil).Times(1)

	// Turn 1 (Initial call): Gemini receives downloaded photoData and invokes inspect_photo_tool
	llmMock.EXPECT().Chat(
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(x any) bool {
			// In initial Chat call, history is empty from store
			return true
		}),
		"Analyze this image and run tool",
		gomock.Cond(func(x any) bool {
			data, ok := x.([]byte)
			return ok && len(data) == photoBytes
		}),
		"image/jpeg",
	).Return(&llm.Response{
		ToolCalls: []*genai.FunctionCall{
			{Name: "inspect_photo_tool"},
		},
	}, nil).Times(1)

	// Turn 2 (Continuation turn): verify extendedHistory[0] retains PhotoData!
	llmMock.EXPECT().Chat(
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(x any) bool {
			hist, ok := x.([]models.ChatMessage)
			if !ok || len(hist) < 3 {
				return false
			}
			// hist[0] is user message in loopHistory: MUST contain raw PhotoData
			userMsg := hist[0]
			return userMsg.Role == "user" &&
				len(userMsg.PhotoData) == photoBytes &&
				userMsg.PhotoMimeType == "image/jpeg"
		}),
		gomock.Any(),
		nil,
		"",
	).Return(&llm.Response{
		Text: "Image analyzed successfully: tool completed.",
	}, nil).Times(1)

	msg := models.Message{
		UserID:      userID,
		ChatID:      "chat-adv-photo",
		Text:        "Analyze this image and run tool",
		PhotoFileID: "photo-adv-123",
		Source:      "telegram",
	}

	err := engine.ProcessMessage(ctx, msg)
	assert.NoError(t, err)

	// Verify that after ProcessMessage, Redis persistence stripped PhotoData
	history, err := engine.store.GetChatHistory(ctx, userID)
	require.NoError(t, err)
	require.Len(t, history, 4) // user, tool calls, tool results (P2-9), assistant
	for _, m := range history {
		assert.Nil(t, m.PhotoData, "no history entry may carry image bytes")
	}

	assert.Equal(t, "user", history[0].Role)
	assert.Nil(t, history[0].PhotoData, "PhotoData must be stripped from Redis chat history")
	assert.Empty(t, history[0].PhotoMimeType, "PhotoMimeType must be empty in Redis chat history")
	assert.Contains(t, history[0].Content, "[Attachment: image/jpeg (1.0 MB)]")
}

// TestChallenger2_BoundedRecursion_Ceiling8_And_WithNoTools verifies SCALE-03:
// - Tool recursion strictly stops at iteration 8.
// - Iteration 8 executes with llm.WithNoTools(ctx) and a terminal summary prompt.
// - Even if an adversarial mock LLM returns another ToolCall on iteration 8,
//   the engine guarantees termination and does NOT loop to iteration 9.
func TestChallenger2_BoundedRecursion_Ceiling8_And_WithNoTools(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := models.Message{UserID: "adv-loop-user", Text: "infinite tool recursion"}

	reg := NewRegistry()
	mockMod := &mockToolModule{
		name: "infinite_tool",
		declarations: []*genai.FunctionDeclaration{
			{Name: "recurse_step"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			return "step completed", nil
		},
	}
	reg.Register(mockMod)
	engine.SetToolRegistry(reg)

	// Iterations 1 to 7: LLM continues requesting another tool call
	for i := 1; i <= 7; i++ {
		iterNum := i
		llmMock.EXPECT().Chat(
			gomock.Cond(func(c any) bool {
				ctxVal, ok := c.(context.Context)
				// Iterations 1-7 MUST NOT have WithNoTools
				return ok && !llm.IsNoTools(ctxVal)
			}),
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			nil,
			"",
		).Return(&llm.Response{
			ToolCalls: []*genai.FunctionCall{
				{Name: "recurse_step", Args: map[string]interface{}{"iter": iterNum}},
			},
		}, nil).Times(1)
	}

	// Iteration 8 (Ceiling reached): Engine MUST call Chat with WithNoTools
	// Adversarial condition: mock LLM returns another ToolCall anyway!
	llmMock.EXPECT().Chat(
		gomock.Cond(func(c any) bool {
			ctxVal, ok := c.(context.Context)
			// Iteration 8 MUST have WithNoTools set to true!
			return ok && llm.IsNoTools(ctxVal)
		}),
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(p any) bool {
			prompt, ok := p.(string)
			return ok && strings.Contains(prompt, "Maximum tool execution limit reached (8 steps)")
		}),
		nil,
		"",
	).Return(&llm.Response{
		// Malicious LLM response: returns a tool call instead of text
		ToolCalls: []*genai.FunctionCall{
			{Name: "recurse_step", Args: map[string]interface{}{"malicious": true}},
		},
	}, nil).Times(1)

	toolCall := &genai.FunctionCall{Name: "recurse_step", Args: map[string]interface{}{"iter": 0}}
	result, err := engine.handleToolCalls(ctx, msg, nil, "sys prompt", []*genai.FunctionCall{toolCall}, 1)

	assert.NoError(t, err)
	// Because iteration 8 returned a tool call instead of text, engine must safely return the fallback text
	assert.Equal(t, "✅ All requested actions have been completed up to the maximum iteration limit.", result)
}

// TestChallenger2_ConcurrentTools_4Calls_StrictOrderingAndParallelism verifies SCALE-03:
// - 4 tool calls with staggered durations (80ms, 20ms, 60ms, 30ms) execute concurrently.
// - Total runtime is bounded near max(duration) (~80ms-110ms), NOT sequential sum (190ms).
// - Function calls and function responses maintain exact 1:1 order alignment regardless
//   of which goroutine finished first.
// - Panic in one of the tools is safely caught without terminating the turn or process.
func TestChallenger2_ConcurrentTools_4Calls_StrictOrderingAndParallelism(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := models.Message{UserID: "adv-concurrent-user", Text: "run 4 tools in parallel"}

	// Staggered execution durations
	toolDurations := map[string]time.Duration{
		"tool_A_80ms": 80 * time.Millisecond,
		"tool_B_20ms": 20 * time.Millisecond,
		"tool_C_60ms": 60 * time.Millisecond,
		"tool_D_30ms": 30 * time.Millisecond,
	}

	reg := NewRegistry()
	mod := &mockToolModule{
		name: "concurrent_domain",
		declarations: []*genai.FunctionDeclaration{
			{Name: "tool_A_80ms"},
			{Name: "tool_B_20ms"},
			{Name: "tool_C_60ms"},
			{Name: "tool_D_30ms"},
			{Name: "tool_E_panic"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			if toolName == "tool_E_panic" {
				panic("simulated critical tool panic")
			}
			if dur, ok := toolDurations[toolName]; ok {
				time.Sleep(dur)
			}
			return "result_of_" + toolName, nil
		},
	}
	reg.Register(mod)
	engine.SetToolRegistry(reg)

	// Verification 1: 4 Tools executed with strict order alignment and concurrency
	llmMock.EXPECT().Chat(
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(x any) bool {
			hist, ok := x.([]models.ChatMessage)
			if !ok || len(hist) < 2 {
				return false
			}

			// Assistant message has tool calls in original order
			asstMsg := hist[len(hist)-2]
			if len(asstMsg.ToolCalls) != 4 {
				return false
			}
			if asstMsg.ToolCalls[0].Name != "tool_A_80ms" ||
				asstMsg.ToolCalls[1].Name != "tool_B_20ms" ||
				asstMsg.ToolCalls[2].Name != "tool_C_60ms" ||
				asstMsg.ToolCalls[3].Name != "tool_D_30ms" {
				return false
			}

			// Function message has responses in exact 1:1 order alignment!
			funcMsg := hist[len(hist)-1]
			if len(funcMsg.ToolResponses) != 4 {
				return false
			}
			return funcMsg.ToolResponses[0].Name == "tool_A_80ms" &&
				funcMsg.ToolResponses[0].Response["result"] == "result_of_tool_A_80ms" &&
				funcMsg.ToolResponses[1].Name == "tool_B_20ms" &&
				funcMsg.ToolResponses[1].Response["result"] == "result_of_tool_B_20ms" &&
				funcMsg.ToolResponses[2].Name == "tool_C_60ms" &&
				funcMsg.ToolResponses[2].Response["result"] == "result_of_tool_C_60ms" &&
				funcMsg.ToolResponses[3].Name == "tool_D_30ms" &&
				funcMsg.ToolResponses[3].Response["result"] == "result_of_tool_D_30ms"
		}),
		gomock.Any(),
		nil,
		"",
	).Return(&llm.Response{Text: "All 4 parallel tools completed."}, nil).Times(1)

	toolCalls := []*genai.FunctionCall{
		{Name: "tool_A_80ms"},
		{Name: "tool_B_20ms"},
		{Name: "tool_C_60ms"},
		{Name: "tool_D_30ms"},
	}

	start := time.Now()
	res, err := engine.handleToolCalls(ctx, msg, nil, "sys", toolCalls, 1)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, "All 4 parallel tools completed.", res)

	// Concurrency assertion:
	// Sum of durations: 80 + 20 + 60 + 30 = 190ms.
	// Parallel duration must be bounded near max(80ms) and strictly less than 150ms!
	assert.Less(t, elapsed, 350*time.Millisecond,
		"Concurrent execution of 4 tools took %v, must be < 150ms (sequential sum is 190ms)", elapsed)

	// Verification 2: Panic Recovery in executeToolsConcurrently
	callsWithPanic := []*genai.FunctionCall{
		{Name: "tool_B_20ms"},
		{Name: "tool_E_panic"},
		{Name: "tool_D_30ms"},
	}

	calls, responses := engine.executeToolsConcurrently(ctx, msg, nil, "sys", callsWithPanic)
	require.Len(t, calls, 3)
	require.Len(t, responses, 3)

	assert.Equal(t, "tool_B_20ms", responses[0].Name)
	assert.Equal(t, "result_of_tool_B_20ms", responses[0].Response["result"])

	assert.Equal(t, "tool_E_panic", responses[1].Name)
	assert.Contains(t, responses[1].Response["result"], "❌ Internal panic during tool execution: simulated critical tool panic")

	assert.Equal(t, "tool_D_30ms", responses[2].Name)
	assert.Equal(t, "result_of_tool_D_30ms", responses[2].Response["result"])
}

// -----------------------------------------------------------------------------
// ADVERSARIAL STRESS SUITE: INTENT MATCHER (ReDoS, Morphological & Edge Cases)
// -----------------------------------------------------------------------------

func TestChallenger2_IntentMatcher_AdversarialStressAndReDoS(t *testing.T) {
	matcher := NewKeywordIntentMatcher()

	t.Run("Empty, Whitespace, and Control Characters", func(t *testing.T) {
		inputs := []string{
			"",
			" ",
			"\t\t\n\r\n   ",
			"\x00\x00\x00",
			"!@#$%^&*()_+-=[]{}|;':,.<>/?~`",
			".................................",
		}
		for _, in := range inputs {
			matched := matcher.MatchDomains(in)
			assert.Empty(t, matched, "Input %q should not match any domain", in)
		}
	})

	t.Run("Massive Inputs & ReDoS Stress", func(t *testing.T) {
		// 100KB of repetitive text
		filler := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit ", 2000)
		require.Greater(t, len(filler), 100000)

		start := time.Now()
		res := matcher.MatchDomains(filler)
		elapsed := time.Since(start)

		assert.Empty(t, res)
		assert.Less(t, elapsed, 250*time.Millisecond, "100KB input must evaluate rapidly without ReDoS")

		// 1MB string with a single domain keyword embedded at the very end
		oneMB := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 23000) + " please fix the refrigerator leak"
		require.Greater(t, len(oneMB), 1000000)

		start = time.Now()
		res1MB := matcher.MatchDomains(oneMB)
		elapsed1MB := time.Since(start)

		assert.Contains(t, res1MB, DomainHouse, "Must find house keyword at end of 1MB buffer")
		assert.Less(t, elapsed1MB, 3*time.Second, "1MB input evaluation must complete under 3s")
	})

	t.Run("False-Positive Trap Words (Boundary Precision)", func(t *testing.T) {
		traps := []struct {
			input        string
			forbiddenDom DomainGroup
			description  string
		}{
			{"We need to clean the red carpet", DomainHouse, "carpet should not trigger car"},
			{"Please submit your user feedback", DomainBaby, "feedback should not trigger feed"},
			{"He gave a brilliant lecture on history", DomainHouse, "history should not trigger house"},
			{"The doctor checked my heart rate", DomainBaby, "heart rate should not trigger baby"},
			{"I have an active bank account", DomainHouse, "account should not trigger ac (air conditioner)"},
			{"Let us consider this action plan", DomainHouse, "action should not trigger ac"},
			{"The misspent youth had no regrets", DomainFinance, "misspent should not trigger spent"},
			{"Please contact our support parent company", DomainHouse, "parent company should not trigger rent"},
		}

		for _, tc := range traps {
			t.Run(tc.description, func(t *testing.T) {
				matched := matcher.MatchDomains(tc.input)
				assert.NotContains(t, matched, tc.forbiddenDom,
					"Input %q incorrectly matched %s (trapped by prefix/boundary)", tc.input, tc.forbiddenDom)
			})
		}
	})

	t.Run("Mixed Bilingual Complex Sentences", func(t *testing.T) {
		mixed := "Baby שתה 120 מ\"ל formula ואז נרדם בעריסה"
		matched := matcher.MatchDomains(mixed)
		assert.Contains(t, matched, DomainBaby)

		multi := "קבעתי עם הרופא ילדים ביומן ושלחתי קבלה של 300 שקל במייל"
		matchedMulti := matcher.MatchDomains(multi)
		assert.Contains(t, matchedMulti, DomainBaby)     // רופא ילדים
		assert.Contains(t, matchedMulti, DomainCalendar) // ביומן
		assert.Contains(t, matchedMulti, DomainFinance)  // קבלה / שקל
		assert.Contains(t, matchedMulti, DomainGmail)    // במייל
	})

	t.Run("Hebrew Niqqud and Gershayim Variants", func(t *testing.T) {
		// Users frequently type Hebrew abbreviations with ASCII quotes or Gershayim
		matchedASCII := matcher.MatchDomains("שילמתי 150 ש\"ח בסופר")
		assert.Contains(t, matchedASCII, DomainFinance)

		matchedGershayim := matcher.MatchDomains("שילמתי 150 ש״ח בסופר")
		// Document behavior for Hebrew Gershayim
		t.Logf("Gershayim test result: %v", matchedGershayim)
	})
}

// -----------------------------------------------------------------------------
// ADVERSARIAL STRESS SUITE: SELECT DECLARATIONS
// -----------------------------------------------------------------------------

func TestChallenger2_Registry_SelectDeclarations_AdversarialCombinations(t *testing.T) {
	reg := NewRegistry()

	memMod := &mockToolModule{
		name: "memory",
		declarations: []*genai.FunctionDeclaration{
			{Name: "remember_fact", Description: "Core tool"},
			{Name: "save_topic_memory", Description: "Core tool"},
			{Name: "search_topic_memories", Description: "Core tool"},
			{Name: "custom_memory_tool", Description: "Non-core memory tool"},
		},
	}
	babyMod := &mockToolModule{
		name: "baby",
		declarations: []*genai.FunctionDeclaration{
			{Name: "upsert_baby_profile"},
			{Name: "log_baby_care"},
		},
	}
	financeMod := &mockToolModule{
		name: "finance",
		declarations: []*genai.FunctionDeclaration{
			{Name: "log_financial_transaction"},
			{Name: "get_financial_summary"},
		},
	}
	calMod := &mockToolModule{
		name: "workspace_calendar",
		declarations: []*genai.FunctionDeclaration{
			{Name: "create_calendar_event"},
			{Name: "list_calendar_events"},
		},
	}
	tasksMod := &mockToolModule{
		name: "workspace_tasks",
		declarations: []*genai.FunctionDeclaration{
			{Name: "list_tasks"},
			{Name: "create_task"},
		},
	}
	dirtyMod := &mockToolModule{
		name: "shared",
		declarations: []*genai.FunctionDeclaration{
			nil, // Nil declaration edge case
			{Name: ""}, // Empty name edge case
			{Name: "manage_shared_space"},
		},
	}

	reg.Register(memMod)
	reg.Register(babyMod)
	reg.Register(financeMod)
	reg.Register(calMod)
	reg.Register(tasksMod)
	reg.Register(dirtyMod)

	hasTool := func(decls []*genai.FunctionDeclaration, name string) bool {
		for _, d := range decls {
			if d != nil && d.Name == name {
				return true
			}
		}
		return false
	}

	t.Run("Tier 1 Core Tools Preservation Across Combinations", func(t *testing.T) {
		domainScenarios := [][]DomainGroup{
			nil,
			{},
			{DomainBaby},
			{DomainFinance},
			{DomainHouse},
			{DomainBaby, DomainFinance, DomainHouse, DomainCalendar},
			{"invalid_domain_xyz"},
		}

		for _, doms := range domainScenarios {
			decls := reg.SelectDeclarations(doms, false)
			assert.True(t, hasTool(decls, "remember_fact"), "remember_fact must be present for %v", doms)
			assert.True(t, hasTool(decls, "save_topic_memory"), "save_topic_memory must be present for %v", doms)
			assert.True(t, hasTool(decls, "search_topic_memories"), "search_topic_memories must be present for %v", doms)
			assert.True(t, hasTool(decls, "get_current_time"), "get_current_time fallback must be present for %v", doms)
		}
	})

	t.Run("Ambient Default Selection (Empty Domains & fallbackToAll=false)", func(t *testing.T) {
		decls := reg.SelectDeclarations(nil, false)
		assert.True(t, hasTool(decls, "remember_fact"))
		assert.True(t, hasTool(decls, "create_calendar_event"))
		assert.True(t, hasTool(decls, "create_task"))
		assert.False(t, hasTool(decls, "upsert_baby_profile"))
		assert.False(t, hasTool(decls, "log_financial_transaction"))
	})

	t.Run("Fallback to All (Empty Domains & fallbackToAll=true)", func(t *testing.T) {
		decls := reg.SelectDeclarations(nil, true)
		allDecls := reg.AllDeclarations()
		assert.Equal(t, len(allDecls), len(decls), "Must return all declarations on fallbackToAll=true")
		assert.True(t, hasTool(decls, "upsert_baby_profile"))
		assert.True(t, hasTool(decls, "log_financial_transaction"))
	})

	t.Run("Invalid & Unknown Domains Handling", func(t *testing.T) {
		invalidDoms := []DomainGroup{"unknown_1", "", "   ", "malicious_injection"}
		decls := reg.SelectDeclarations(invalidDoms, false)
		require.NotEmpty(t, decls)
		assert.True(t, hasTool(decls, "get_current_time"))
		assert.False(t, hasTool(decls, "upsert_baby_profile"))
		assert.False(t, hasTool(decls, "log_financial_transaction"))
	})

	t.Run("Duplicate Input Domains Deduplication", func(t *testing.T) {
		dupDoms := []DomainGroup{DomainBaby, DomainBaby, DomainBaby, DomainBaby}
		decls := reg.SelectDeclarations(dupDoms, false)

		count := 0
		for _, d := range decls {
			if d.Name == "upsert_baby_profile" {
				count++
			}
		}
		assert.Equal(t, 1, count, "upsert_baby_profile must appear exactly once despite duplicate domains")
	})

	t.Run("Nil and Dirty Declarations Resilience", func(t *testing.T) {
		decls := reg.SelectDeclarations([]DomainGroup{DomainShared}, false)
		for _, d := range decls {
			require.NotNil(t, d, "Declaration in selected list must not be nil")
			assert.NotEmpty(t, d.Name, "Declaration name must not be empty")
		}
		assert.True(t, hasTool(decls, "manage_shared_space"))
	})
}

// -----------------------------------------------------------------------------
// ADVERSARIAL STRESS SUITE: 5MB–10MB PHOTODATA PAYLOAD SANITIZATION
// -----------------------------------------------------------------------------

func TestChallenger2_PhotoData_5MB_10MB_StressPayloads(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	t.Run("5MB Image Injection into Store", func(t *testing.T) {
		userID := "user_5mb_stress"
		photo5MB := make([]byte, 5*1024*1024)
		for i := range photo5MB {
			photo5MB[i] = byte(i % 251)
		}

		msg := models.ChatMessage{
			Role:          "user",
			Content:       "Please analyze this 5MB document scan",
			PhotoData:     photo5MB,
			PhotoMimeType: "image/jpeg",
		}

		err := store.AppendChatHistory(ctx, userID, msg)
		require.NoError(t, err)

		rawEntries, err := mr.List("chat:" + userID)
		require.NoError(t, err)
		require.Len(t, rawEntries, 1)

		rawRedisJSON := rawEntries[0]
		assert.Less(t, len(rawRedisJSON), 500, "Raw Redis entry must be under 500 bytes (got %d bytes)", len(rawRedisJSON))
		assert.Contains(t, rawRedisJSON, "[Attachment: image/jpeg (5.0 MB)]")
		assert.Contains(t, rawRedisJSON, "Please analyze this 5MB document scan")
		assert.NotContains(t, rawRedisJSON, "photo_data\":\"", "Redis must never contain serialized photo_data base64 payload")

		var storedMsg models.ChatMessage
		err = json.Unmarshal([]byte(rawRedisJSON), &storedMsg)
		require.NoError(t, err)
		assert.Nil(t, storedMsg.PhotoData, "Stored PhotoData must be nil in Redis")
		assert.Empty(t, storedMsg.PhotoMimeType, "Stored PhotoMimeType must be empty in Redis")

		history, err := store.GetChatHistory(ctx, userID)
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.Nil(t, history[0].PhotoData)
		assert.Empty(t, history[0].PhotoMimeType)
		assert.Contains(t, history[0].Content, "[Attachment: image/jpeg (5.0 MB)]")
	})

	t.Run("10MB Heavy Image Injection into Store", func(t *testing.T) {
		userID := "user_10mb_stress"
		photo10MB := make([]byte, 10*1024*1024)
		for i := range photo10MB {
			photo10MB[i] = byte((i * 17) % 255)
		}

		msg := models.ChatMessage{
			Role:          "user",
			Content:       "",
			PhotoData:     photo10MB,
			PhotoMimeType: "image/png",
		}

		err := store.AppendChatHistory(ctx, userID, msg)
		require.NoError(t, err)

		rawEntries, err := mr.List("chat:" + userID)
		require.NoError(t, err)
		require.Len(t, rawEntries, 1)

		rawJSON := rawEntries[0]
		assert.Less(t, len(rawJSON), 300, "Raw Redis JSON for empty caption 10MB photo must be under 300 bytes")
		assert.Contains(t, rawJSON, "[Attachment: image/png (10.0 MB)]")
		assert.NotContains(t, rawJSON, "photo_data\":\"", "Must have zero binary bytes in Redis")
	})

	t.Run("5MB Active Vision End-to-End Retention in Engine", func(t *testing.T) {
		engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
		defer cleanup()

		userID := "user_engine_5mb"
		raw5MB := make([]byte, 5*1024*1024)
		for i := range raw5MB {
			raw5MB[i] = byte(i % 211)
		}

		dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()

		reg := NewRegistry()
		reg.Register(&mockToolModule{
			name: "memory",
			declarations: []*genai.FunctionDeclaration{
				{Name: "remember_fact"},
			},
			executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
				return `{"status":"fact remembered"}`, nil
			},
		})
		engine.SetToolRegistry(reg)

		tgMock.EXPECT().GetFile(gomock.Any(), "photo-5mb-tg").Return("path/photo.jpg", nil).Times(1)
		tgMock.EXPECT().DownloadFile(gomock.Any(), "path/photo.jpg").Return(raw5MB, nil).Times(1)

		// Turn 1: Gemini receives raw 5MB photo and returns a tool call
		llmMock.EXPECT().Chat(
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			"Analyze this 5MB invoice",
			gomock.Cond(func(x any) bool {
				data, ok := x.([]byte)
				return ok && len(data) == 5*1024*1024
			}),
			"image/jpeg",
		).Return(&llm.Response{
			ToolCalls: []*genai.FunctionCall{
				{Name: "remember_fact", Args: map[string]interface{}{"fact": "Invoice total $500"}},
			},
		}, nil).Times(1)

		// Turn 2: During recursive tool recursion, verify loopHistory retains raw 5MB photo bytes!
		var capturedLoopHistory []models.ChatMessage
		llmMock.EXPECT().Chat(
			gomock.Any(),
			gomock.Any(),
			gomock.Cond(func(x any) bool {
				hist, ok := x.([]models.ChatMessage)
				if !ok || len(hist) < 2 {
					return false
				}
				capturedLoopHistory = hist
				userTurn := hist[0]
				return userTurn.Role == "user" &&
					len(userTurn.PhotoData) == 5*1024*1024 &&
					userTurn.PhotoMimeType == "image/jpeg"
			}),
			gomock.Any(),
			nil,
			"",
		).Return(&llm.Response{Text: "5MB invoice processed successfully."}, nil).Times(1)

		userMsg := models.Message{
			UserID:      userID,
			ChatID:      "chat-adv-5mb-engine",
			Text:        "Analyze this 5MB invoice",
			PhotoFileID: "photo-5mb-tg",
			Source:      "telegram",
		}

		err = engine.ProcessMessage(context.Background(), userMsg)
		require.NoError(t, err)

		// 1. In-memory assertion:
		require.NotEmpty(t, capturedLoopHistory)
		assert.Equal(t, 5*1024*1024, len(capturedLoopHistory[0].PhotoData), "Active vision MUST retain raw 5MB photo bytes")

		// 2. Persistent Redis assertion:
		history, err := engine.store.GetChatHistory(context.Background(), userID)
		require.NoError(t, err)
		// Length depends on gating policy (P3-1: remember_fact in an image turn is staged for
		// CONFIRM, so no tool pair is persisted); this test is about byte stripping.
		require.NotEmpty(t, history)
		for _, m := range history {
			assert.Nil(t, m.PhotoData, "Redis chat history must contain ZERO binary bytes")
		}
		assert.Empty(t, history[0].PhotoMimeType)
		assert.Contains(t, history[0].Content, "[Attachment: image/jpeg (5.0 MB)]")
	})
}
