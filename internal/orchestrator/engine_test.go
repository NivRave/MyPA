package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	tgmocks "github.com/nivik/mypa/internal/telegram/mocks"
	"github.com/nivik/mypa/internal/twilio"
	wamocks "github.com/nivik/mypa/internal/twilio/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

// whatsAppMocks remembers the WhatsApp API mock behind each test engine: the engine holds only the
// channel registry (V4-31), so tests that script WhatsApp look the mock up here.
var whatsAppMocks sync.Map

func whatsAppMockOf(t *testing.T, e *Engine) *wamocks.MockAPI {
	t.Helper()
	m, ok := whatsAppMocks.Load(e)
	require.True(t, ok, "engine was not built by setupTestEngine")
	return m.(*wamocks.MockAPI)
}

func setupTestEngine(t *testing.T) (*Engine, *tgmocks.MockAPI, *mocks.MockLLMClient, *mocks.MockDBClient, func()) {
	ctrl := gomock.NewController(t)

	tgMock := tgmocks.NewMockAPI(ctrl)
	twMock := wamocks.NewMockAPI(ctrl)
	audioMock := mocks.NewMockAudioClient(ctrl)
	llmMock := mocks.NewMockLLMClient(ctrl)
	gmailMock := mocks.NewMockGmailClient(ctrl)
	tasksMock := mocks.NewMockTasksClient(ctrl)
	tavilyMock := mocks.NewMockTavilyClient(ctrl)
	dbMock := mocks.NewMockDBClient(ctrl)
	eventPublisherMock := mocks.NewMockEventPublisher(ctrl)
	eventPublisherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	// Setup in-memory Redis
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	store, err := state.NewStore("redis://" + mr.Addr())
	assert.NoError(t, err)

	// We don't need real DB in tests since we inject dbMock
	// dbClient, err := db.NewClient(":memory:")
	// assert.NoError(t, err)
	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	})

	engine := NewEngine(
		nil, // consumer not needed for processMessage tests
		store,
		dbMock,
		llmMock,
		adapterstest.Registry(tgMock, twMock),
		oauthCfg,
		gmailMock,
		tasksMock,
		nil, // contactsMock
		tavilyMock,
		audioMock,
		eventPublisherMock,
		"UTC",
	)
	whatsAppMocks.Store(engine, twMock)
	t.Cleanup(func() { whatsAppMocks.Delete(engine) })

	reg := NewRegistry()
	reg.Register(&testBaseToolsModule{engine: engine})
	engine.SetToolRegistry(reg)

	expectHouseholds(dbMock)
	dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, userID string) (*models.User, error) {
		return inHousehold(&models.User{PlatformID: userID, Name: "User " + userID}), nil
	}).AnyTimes()
	// Tools invoked outside a turn look the user up by ID (turn.go's fallback).
	dbMock.EXPECT().GetUser(gomock.Any()).DoAndReturn(func(userID string) (*models.User, error) {
		return inHousehold(&models.User{PlatformID: userID, Name: "User " + userID}), nil
	}).AnyTimes()

	cleanup := func() {
		engine.Wait()
		store.Close()
		mr.Close()
		ctrl.Finish()
	}

	return engine, tgMock, llmMock, dbMock, cleanup
}

type testBaseToolsModule struct {
	engine *Engine
}

func (m *testBaseToolsModule) Name() string { return "test_base_tools" }
func (m *testBaseToolsModule) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{Name: "create_calendar_event"},
		{Name: "create_task"},
		{Name: "search_web"},
		{Name: "search_emails"},
		{Name: "schedule_reminder"},
	}
}
func (m *testBaseToolsModule) SystemPromptSection(ctx context.Context, callCtx CallContext) string {
	return ""
}
func (m *testBaseToolsModule) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
	switch toolName {
	case "create_calendar_event":
		if m.engine.calendarFactory != nil {
			client, err := m.engine.calendarFactory(ctx, callCtx.ActorID())
			if err != nil {
				return "", err
			}
			link, err := client.CreateEvent(ctx, models.CalendarEvent{})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Event Created Successfully! Link: %s", link), nil
		}
	case "create_task":
		if m.engine.tasksClient != nil {
			listID, _ := args["list_id"].(string)
			title, _ := args["title"].(string)
			notes, _ := args["notes"].(string)
			due, _ := args["due"].(string)
			err := m.engine.tasksClient.CreateTask(ctx, callCtx.ActorID(), listID, title, notes, due)
			if err != nil {
				return "", err
			}
			return "Task created successfully.", nil
		}
	case "search_web":
		if m.engine.tavilyClient != nil {
			query, _ := args["query"].(string)
			if callCtx.SendNotification != nil {
				_ = callCtx.SendNotification(ctx, callCtx.ActorID(), "🔍 Searching the web...")
			}
			return m.engine.tavilyClient.Search(ctx, query)
		}
	case "search_emails":
		if m.engine.gmailClient != nil {
			query, _ := args["query"].(string)
			emails, err := m.engine.gmailClient.SearchEmails(ctx, callCtx.ActorID(), query, int64(5))
			if err != nil {
				return "", err
			}
			if len(emails) == 0 {
				return "You have no unread emails.", nil
			}
			return "Found emails", nil
		}
	case "schedule_reminder":
		msgStr, _ := args["message"].(string)
		dueTimeStr, _ := args["due_time"].(string)
		dueTime, _ := time.Parse(time.RFC3339, dueTimeStr)
		err := m.engine.db.SaveReminder(models.ScheduledReminder{
			UserID:  callCtx.ActorID(),
			Message: msgStr,
			DueTime: dueTime,
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ **Reminder Set!**\n\nI will remind you at %s: \"%s\"", dueTime.In(time.Local).Format(time.RFC822), msgStr), nil
	}
	return "", nil
}

func TestProcessMessage_ConnectCommand(t *testing.T) {
	engine, tgMock, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	msg := channel.Inbound{
		MessageID:    "msg-1",
		Sender:       "user-1",
		Conversation: channel.Conversation{ID: "chat-1"},
		Text:         "/connect",
		Channel:      "telegram",
		SentAt:       time.Now(),
	}

	// We expect the bot to reply with a connection link
	tgMock.EXPECT().
		SendMessage(gomock.Any(), "chat-1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, chatID, text string) error {
			assert.Contains(t, text, "Click here to connect your Google Calendar")
			return nil
		}).
		Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestProcessMessage_TextMessage(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	msg := channel.Inbound{
		MessageID:    "msg-2",
		Sender:       "user-1",
		Conversation: channel.Conversation{ID: "chat-1"},
		Text:         "Hello, bot!",
		Channel:      "telegram",
		SentAt:       time.Now(),
	}

	// Mock DB calls for history and memories
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.TopicMemory{}, nil).AnyTimes()

	llmResponse := &llm.Response{
		Text: "Hello there!",
	}
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(llmResponse, nil).Times(1)

	tgMock.EXPECT().SendMessage(gomock.Any(), "chat-1", "Hello there!").Return(nil).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestProcessMessage_WhatsApp_TextDocument(t *testing.T) {
	engine, _, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	twMock := whatsAppMockOf(t, engine)

	msg := channel.Inbound{
		MessageID:    "wa-msg-1",
		Sender:       "+1234567890",
		Conversation: channel.Conversation{ID: "+1234567890"},
		Text:         "Please setup my daily checklist:",
		Channel:      "whatsapp",
		Attachments:  []channel.AttachmentRef{twilio.MediaRef("https://api.twilio.com/media/doc123", "text/plain")},
		SentAt:       time.Now(),
	}

	docContent := "Habit 1: Drink water\nHabit 2: Stretch"
	twMock.EXPECT().DownloadMedia(gomock.Any(), "https://api.twilio.com/media/doc123").Return([]byte(docContent), nil).Times(1)

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.TopicMemory{}, nil).AnyTimes()

	llmResponse := &llm.Response{
		Text: "Checklist received and scheduled!",
	}
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Nil(), "").
		DoAndReturn(func(ctx context.Context, sys string, hist []models.ChatMessage, newMsg string, photo []byte, mime string) (*llm.Response, error) {
			assert.Contains(t, newMsg, "Please setup my daily checklist:")
			assert.Contains(t, newMsg, "[Attached Document Content]:")
			assert.Contains(t, newMsg, docContent)
			return llmResponse, nil
		}).Times(1)

	twMock.EXPECT().SendMessage(gomock.Any(), "+1234567890", "Checklist received and scheduled!").Return(nil).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestProcessMessage_WhatsApp_PDFDocument(t *testing.T) {
	engine, _, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	twMock := whatsAppMockOf(t, engine)

	msg := channel.Inbound{
		MessageID:    "wa-msg-2",
		Sender:       "+1234567890",
		Conversation: channel.Conversation{ID: "+1234567890"},
		Text:         "",
		Channel:      "whatsapp",
		Attachments:  []channel.AttachmentRef{twilio.MediaRef("https://api.twilio.com/media/pdf123", "application/pdf")},
		SentAt:       time.Now(),
	}

	pdfBytes := []byte("%PDF-1.4 mock content")
	twMock.EXPECT().DownloadMedia(gomock.Any(), "https://api.twilio.com/media/pdf123").Return(pdfBytes, nil).Times(1)

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.TopicMemory{}, nil).AnyTimes()

	llmResponse := &llm.Response{
		Text: "Document analyzed!",
	}
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), "Please process and analyze the attached document.", pdfBytes, "application/pdf").
		Return(llmResponse, nil).Times(1)

	twMock.EXPECT().SendMessage(gomock.Any(), "+1234567890", "Document analyzed!").Return(nil).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestProcessMessage_WhatsApp_UnsupportedMedia(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	twMock := whatsAppMockOf(t, engine)

	msg := channel.Inbound{
		MessageID:    "wa-msg-3",
		Sender:       "+1234567890",
		Conversation: channel.Conversation{ID: "+1234567890"},
		Text:         "",
		Channel:      "whatsapp",
		Attachments:  []channel.AttachmentRef{twilio.MediaRef("https://api.twilio.com/media/vid123", "video/mp4")},
		SentAt:       time.Now(),
	}

	twMock.EXPECT().SendMessage(gomock.Any(), "+1234567890", gomock.Any()).
		DoAndReturn(func(ctx context.Context, to, text string) error {
			assert.Contains(t, text, "Unsupported attachment format")
			return nil
		}).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestNormalizeHebrewPrepositionNumbers(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "תזכיר לי ב18 לנקות את השואב",
			expected: "תזכיר לי ב-18 לנקות את השואב",
		},
		{
			input:    "פגישה ב10:30 בבוקר",
			expected: "פגישה ב-10:30 בבוקר",
		},
		{
			input:    "ב18",
			expected: "ב-18",
		},
		{
			input:    "ב-18",
			expected: "ב-18",
		},
		{
			input:    "מ5 עד 10",
			expected: "מ-5 עד 10",
		},
		{
			input:    "ל20 אנשים",
			expected: "ל-20 אנשים",
		},
		{
			input:    "כ50 אחוז",
			expected: "כ-50 אחוז",
		},
		{
			input:    "רכב18",
			expected: "רכב18",
		},
	}

	for _, tt := range tests {
		actual := NormalizeHebrewPrepositionNumbers(tt.input)
		assert.Equal(t, tt.expected, actual, "for input: %s", tt.input)
	}
}

func TestProcessMessage_ContentBlocked_RetrySuccess(t *testing.T) {
	engine, _, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	twMock := whatsAppMockOf(t, engine)

	msg := channel.Inbound{
		MessageID:    "wa-blocked-1",
		Sender:       "+1234567890",
		Conversation: channel.Conversation{ID: "+1234567890"},
		Text:         "תזכיר לי ב18 לנקות את השואב",
		Channel:      "whatsapp",
		SentAt:       time.Now(),
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.TopicMemory{}, nil).AnyTimes()

	// 1st call fails with ContentBlockedError
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), "תזכיר לי ב18 לנקות את השואב", gomock.Any(), gomock.Any()).
		Return(nil, &llm.ContentBlockedError{BlockReason: "PROHIBITED_CONTENT"}).Times(1)

	// 2nd call with normalized text succeeds
	llmResponse := &llm.Response{
		Text: "רשמתי תזכורת!",
	}
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), "תזכיר לי ב-18 לנקות את השואב", gomock.Any(), gomock.Any()).
		Return(llmResponse, nil).Times(1)

	twMock.EXPECT().SendMessage(gomock.Any(), "+1234567890", "רשמתי תזכורת!").Return(nil).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestProcessMessage_ContentBlocked_PersistentError(t *testing.T) {
	engine, _, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	twMock := whatsAppMockOf(t, engine)

	msg := channel.Inbound{
		MessageID:    "wa-blocked-2",
		Sender:       "+1234567890",
		Conversation: channel.Conversation{ID: "+1234567890"},
		Text:         "some blocked text without numbers",
		Channel:      "whatsapp",
		SentAt:       time.Now(),
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.TopicMemory{}, nil).AnyTimes()

	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), "some blocked text without numbers", gomock.Any(), gomock.Any()).
		Return(nil, &llm.ContentBlockedError{BlockReason: "SAFETY"}).Times(1)

	twMock.EXPECT().SendMessage(gomock.Any(), "+1234567890", gomock.Any()).
		DoAndReturn(func(ctx context.Context, to, text string) error {
			assert.Contains(t, text, "AI safety filter flagged this request")
			return nil
		}).Times(1)

	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

func TestEngine_HandleToolCall_RegistryDelegation(t *testing.T) {
	engine, _, _, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	reg := NewRegistry()
	mod := &mockToolModule{
		name: "test_domain",
		declarations: []*genai.FunctionDeclaration{
			{Name: "custom_registry_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			assert.Equal(t, "custom_registry_tool", toolName)
			assert.Equal(t, "hello", args["greeting"])
			return "registry execution success", nil
		},
		promptFunc: func(ctx context.Context, callCtx CallContext) string {
			return "Custom domain prompt section."
		},
	}
	reg.Register(mod)
	engine.SetToolRegistry(reg)

	dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), "user-1").Return(inHousehold(&models.User{PlatformID: "user-1", Name: "User One"}), nil).AnyTimes()

	msg := channel.Inbound{Sender: "user-1", Text: "run custom tool"}
	tc := &genai.FunctionCall{
		Name: "custom_registry_tool",
		Args: map[string]interface{}{"greeting": "hello"},
	}

	res := engine.executeSingleTool(context.Background(), msg, nil, "system", tc)
	assert.Equal(t, "registry execution success", res)

	// Unknown tool still returns unknown tool warning
	unknownTc := &genai.FunctionCall{
		Name: "unregistered_tool",
		Args: map[string]interface{}{},
	}
	resUnknown := engine.executeSingleTool(context.Background(), msg, nil, "system", unknownTc)
	assert.Equal(t, "⚠️ LLM tried to call an unknown tool.", resUnknown)
}

func setupUnauthTestEngine(t *testing.T) (*Engine, *tgmocks.MockAPI, *mocks.MockDBClient, func()) {
	ctrl := gomock.NewController(t)

	tgMock := tgmocks.NewMockAPI(ctrl)
	twMock := wamocks.NewMockAPI(ctrl)
	audioMock := mocks.NewMockAudioClient(ctrl)
	llmMock := mocks.NewMockLLMClient(ctrl)
	gmailMock := mocks.NewMockGmailClient(ctrl)
	tasksMock := mocks.NewMockTasksClient(ctrl)
	tavilyMock := mocks.NewMockTavilyClient(ctrl)
	dbMock := mocks.NewMockDBClient(ctrl)
	eventPublisherMock := mocks.NewMockEventPublisher(ctrl)
	eventPublisherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	mr, err := miniredis.Run()
	assert.NoError(t, err)
	store, err := state.NewStore("redis://" + mr.Addr())
	assert.NoError(t, err)

	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	})

	engine := NewEngine(
		nil,
		store,
		dbMock,
		llmMock,
		adapterstest.Registry(tgMock, twMock),
		oauthCfg,
		gmailMock,
		tasksMock,
		nil,
		tavilyMock,
		audioMock,
		eventPublisherMock,
		"UTC",
	)
	whatsAppMocks.Store(engine, twMock)
	t.Cleanup(func() { whatsAppMocks.Delete(engine) })
	expectHouseholds(dbMock)

	cleanup := func() {
		engine.Wait()
		store.Close()
		mr.Close()
		ctrl.Finish()
	}

	return engine, tgMock, dbMock, cleanup
}

func TestProcessMessage_PairingRateLimitAndLockout(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	// Override GetUser to simulate unauthorized user
	dbMock.EXPECT().UserForAccount(gomock.Any(), gomock.Any(), "unauth-user").Return(nil, assert.AnError).AnyTimes()
	dbMock.EXPECT().GetPairingCode("000000").Return(nil, assert.AnError).AnyTimes()

	// 1-4 failures
	for i := 1; i <= 4; i++ {
		expectedRemaining := 5 - i
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			str, ok := x.(string)
			return ok && strings.Contains(str, fmt.Sprintf("%d attempts remaining", expectedRemaining))
		})).Return(nil).Times(1)

		msg := channel.Inbound{Sender: "unauth-user", Text: "000000", Channel: "telegram"}
		err := engine.ProcessMessage(context.Background(), msg)
		assert.NoError(t, err)
	}

	// 5th failure triggers lockout message
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "locked for 1 hour")
	})).Return(nil).Times(1)

	msg := channel.Inbound{Sender: "unauth-user", Text: "000000", Channel: "telegram"}
	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)

	// 6th message while locked out returns temporarily locked out
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "temporarily locked out")
	})).Return(nil).Times(1)

	msg = channel.Inbound{Sender: "unauth-user", Text: "any text", Channel: "telegram"}
	err = engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}

type testRollbackModule struct {
	lastActorID    string
	lastMutationID string
}

func (m *testRollbackModule) Name() string                               { return "durability" }
func (m *testRollbackModule) Declarations() []*genai.FunctionDeclaration { return nil }
func (m *testRollbackModule) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
	return "", nil
}
func (m *testRollbackModule) SystemPromptSection(ctx context.Context, callCtx CallContext) string {
	return ""
}
func (m *testRollbackModule) Rollback(ctx context.Context, s identity.Scope, mutationID string, reason ...string) (*models.RollbackResult, error) {
	m.lastActorID = s.PersonKey()
	m.lastMutationID = mutationID
	return &models.RollbackResult{
		MutationID: mutationID,
		Detail:     "Reverted test action",
	}, nil
}
func (m *testRollbackModule) RollbackLast(ctx context.Context, s identity.Scope, reason ...string) (*models.RollbackResult, error) {
	return m.Rollback(ctx, s, "")
}

func TestProcessMessage_UndoCommand(t *testing.T) {
	engine, tgMock, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	rbMod := &testRollbackModule{}
	reg := NewRegistry()
	reg.Register(rbMod)
	engine.WithToolRegistry(reg)

	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "Successfully Undone") && strings.Contains(str, "Reverted test action")
	})).Return(nil).Times(1)

	msg := channel.Inbound{Sender: "user-1", Text: "/undo mut-123", Channel: "telegram"}
	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
	assert.Equal(t, "user-1", rbMod.lastActorID)
	assert.Equal(t, "mut-123", rbMod.lastMutationID)
}

func TestCheckAndSendReminders_SharedTaskReminders(t *testing.T) {
	engine, tgMock, _, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	now := time.Now()
	sharedDue := []models.SharedTaskReminder{
		{
			ID:               42,
			HouseholdID:      householdID("fam-1"),
			FamilyGroup:      "fam-1",
			Topic:            "baby",
			Title:            "Buy diapers",
			DueTime:          &now,
			CreatedByUserID:  "user-mom",
			AssignedToUserID: "user-dad",
		},
	}

	dbMock.EXPECT().GetDueReminders().Return(nil, nil)
	dbMock.EXPECT().GetDueSharedReminders().Return(sharedDue, nil)
	dbMock.EXPECT().GetLastAuditSessionForUser("user-dad").Return(&models.AuditSession{
		ChatID: "chat-dad",
		Source: "telegram",
	}, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), "chat-dad", gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "Shared Task Reminder") && strings.Contains(str, "Buy diapers") && strings.Contains(str, "baby")
	})).Return(nil)
	dbMock.EXPECT().MarkSharedReminderSent(gomock.Any(), uint(42)).Return(nil)

	engine.CheckAndSendReminders()
}

func TestProcessMessage_PhotoDataStrippedFromRedis(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	userID := "user-photo-strip"
	photo := []byte("fake-photo-data-bytes-for-receipt")

	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	tgMock.EXPECT().GetFile(gomock.Any(), "photo-123").Return("path/to/photo.jpg", nil)
	tgMock.EXPECT().DownloadFile(gomock.Any(), "path/to/photo.jpg").Return(photo, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	// Verify LLM receives photoData during active turn
	llmMock.EXPECT().Chat(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		"Here is my receipt",
		photo,
		"image/jpeg",
	).Return(&llm.Response{Text: "Receipt processed."}, nil)

	msg := channel.Inbound{
		Sender:       userID,
		Conversation: channel.Conversation{ID: "chat-photo"},
		Text:         "Here is my receipt",
		Channel:      "telegram",
		Attachments:  []channel.AttachmentRef{{ID: "photo-123", Kind: channel.KindImage, ContentType: "image/jpeg", Label: "telegram_photo"}},
	}

	err := engine.ProcessMessage(ctx, msg)
	assert.NoError(t, err)

	// Inspect persistent Redis chat history: PhotoData must be stripped
	history, err := engine.store.GetChatHistory(ctx, userID)
	assert.NoError(t, err)
	assert.Len(t, history, 2) // user + assistant

	assert.Equal(t, "user", history[0].Role)
	assert.Nil(t, history[0].PhotoData)
	assert.Empty(t, history[0].PhotoMimeType)
	assert.Contains(t, history[0].Content, "Here is my receipt")
	assert.Contains(t, history[0].Content, "[Attachment: image/jpeg")
}

func TestHandleToolCalls_BoundedRecursion(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := channel.Inbound{Sender: "user-loop", Text: "do recursive actions"}

	reg := NewRegistry()
	mockMod := &mockToolModule{
		name: "test_loop",
		declarations: []*genai.FunctionDeclaration{
			{Name: "loop_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			return "step done", nil
		},
	}
	reg.Register(mockMod)
	engine.SetToolRegistry(reg)

	// LLM keeps returning tool calls for 7 iterations
	for i := 1; i <= 7; i++ {
		llmMock.EXPECT().Chat(
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			nil,
			"",
		).Return(&llm.Response{
			ToolCalls: []*genai.FunctionCall{
				{Name: "loop_tool", Args: map[string]interface{}{"query": "recursive step"}},
			},
		}, nil).Times(1)
	}

	// Iteration 8 (maximum reached): Engine must invoke terminal summary with WithNoTools
	llmMock.EXPECT().Chat(
		gomock.Cond(func(x any) bool {
			c, ok := x.(context.Context)
			return ok && llm.IsNoTools(c)
		}),
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(x any) bool {
			prompt, ok := x.(string)
			return ok && strings.Contains(prompt, "Maximum tool execution limit reached")
		}),
		nil,
		"",
	).Return(&llm.Response{Text: "Final summary: 8 iterations executed."}, nil).Times(1)

	toolCall := &genai.FunctionCall{Name: "loop_tool", Args: map[string]interface{}{"query": "start"}}
	result, err := engine.handleToolCalls(ctx, msg, nil, "system prompt", []*genai.FunctionCall{toolCall}, 1)
	assert.NoError(t, err)
	assert.Equal(t, "Final summary: 8 iterations executed.", result)
}

func TestHandleToolCalls_ConcurrentExecution(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := channel.Inbound{Sender: "user-parallel", Text: "parallel actions"}

	// Register 3 custom tools that take 50ms each
	reg := NewRegistry()
	customMod := &mockToolModule{
		name: "concurrent_tools",
		declarations: []*genai.FunctionDeclaration{
			{Name: "slow_tool_1"},
			{Name: "slow_tool_2"},
			{Name: "slow_tool_3"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			time.Sleep(50 * time.Millisecond)
			return "done " + toolName, nil
		},
	}
	reg.Register(customMod)
	engine.SetToolRegistry(reg)

	// In the continuation turn, verify extendedHistory contains assistant tool calls and function responses in exact 1:1 order
	llmMock.EXPECT().Chat(
		gomock.Any(),
		gomock.Any(),
		gomock.Cond(func(x any) bool {
			history, ok := x.([]models.ChatMessage)
			if !ok || len(history) < 2 {
				return false
			}
			// Last message is function responses
			funcMsg := history[len(history)-1]
			if funcMsg.Role != "function" || len(funcMsg.ToolResponses) != 3 {
				return false
			}
			return funcMsg.ToolResponses[0].Name == "slow_tool_1" &&
				funcMsg.ToolResponses[1].Name == "slow_tool_2" &&
				funcMsg.ToolResponses[2].Name == "slow_tool_3" &&
				funcMsg.ToolResponses[0].Response["result"] == "done slow_tool_1" &&
				funcMsg.ToolResponses[1].Response["result"] == "done slow_tool_2" &&
				funcMsg.ToolResponses[2].Response["result"] == "done slow_tool_3"
		}),
		gomock.Any(),
		nil,
		"",
	).Return(&llm.Response{Text: "All 3 tools executed."}, nil).Times(1)

	toolCalls := []*genai.FunctionCall{
		{Name: "slow_tool_1"},
		{Name: "slow_tool_2"},
		{Name: "slow_tool_3"},
	}

	start := time.Now()
	res, err := engine.handleToolCalls(ctx, msg, nil, "system", toolCalls, 1)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, "All 3 tools executed.", res)
	// If sequential: 3 * 50ms = 150ms+. If parallel: ~50-80ms.
	assert.Less(t, elapsed, 130*time.Millisecond, "Parallel execution must run concurrently in < 130ms")
}

func TestProcessMessage_TwoTierToolScoping(t *testing.T) {
	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()

	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	// Register baby and finance modules
	reg := NewRegistry()
	babyMod := &mockToolModule{
		name: "baby",
		declarations: []*genai.FunctionDeclaration{
			{Name: "upsert_baby_profile"},
			{Name: "log_baby_care"},
		},
	}
	finMod := &mockToolModule{
		name: "finance",
		declarations: []*genai.FunctionDeclaration{
			{Name: "log_financial_transaction"},
		},
	}
	reg.Register(babyMod)
	reg.Register(finMod)
	engine.SetToolRegistry(reg)

	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()

	// When user mentions baby, LLM must receive baby tools via WithActiveTools
	llmMock.EXPECT().Chat(
		gomock.Cond(func(x any) bool {
			c, ok := x.(context.Context)
			if !ok {
				return false
			}
			tools, ok := llm.ActiveToolsFromContext(c)
			if !ok {
				return false
			}
			toolNames := make(map[string]bool)
			for _, d := range tools {
				toolNames[d.Name] = true
			}
			// Should contain baby tool and core tool, but NOT finance tool
			return toolNames["log_baby_care"] && toolNames["get_current_time"] && !toolNames["log_financial_transaction"]
		}),
		gomock.Any(),
		gomock.Any(),
		"Liam ate 120ml of formula",
		nil,
		"",
	).Return(&llm.Response{Text: "Logged baby feeding."}, nil).Times(1)

	msg := channel.Inbound{
		Sender:       "user-baby-1",
		Conversation: channel.Conversation{ID: "chat-1"},
		Text:         "Liam ate 120ml of formula",
		Channel:      "telegram",
	}

	err := engine.ProcessMessage(ctx, msg)
	assert.NoError(t, err)
}

func TestExecuteToolsConcurrently_PanicRecovery(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	msg := channel.Inbound{Sender: "user-panic-test"}

	reg := NewRegistry()
	testMod := &mockToolModule{
		name: "test_panic_mod",
		declarations: []*genai.FunctionDeclaration{
			{Name: "good_tool"},
			{Name: "panicking_tool"},
		},
		executeFunc: func(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error) {
			if toolName == "panicking_tool" {
				panic("simulated fatal crash in tool handler")
			}
			return "good tool success", nil
		},
	}
	reg.Register(testMod)
	engine.SetToolRegistry(reg)

	toolCalls := []*genai.FunctionCall{
		{Name: "good_tool"},
		{Name: "panicking_tool"},
	}

	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)

	require.Len(t, funcCalls, 2)
	require.Len(t, funcResponses, 2)

	assert.Equal(t, "good_tool", funcResponses[0].Name)
	assert.Equal(t, "good tool success", funcResponses[0].Response["result"])

	assert.Equal(t, "panicking_tool", funcResponses[1].Name)
	assert.Contains(t, funcResponses[1].Response["result"], "Internal panic during tool execution")
}

func TestExecuteToolsConcurrently_ContextCancellation(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel context

	msg := channel.Inbound{Sender: "user-cancel-test"}
	toolCalls := []*genai.FunctionCall{
		{Name: "tool_1"},
		{Name: "tool_2"},
	}

	funcCalls, funcResponses := engine.executeToolsConcurrently(ctx, msg, nil, "system", toolCalls)
	require.Len(t, funcCalls, 2)
	require.Len(t, funcResponses, 2)
}
