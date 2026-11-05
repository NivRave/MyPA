package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

func setupTestEngine(t *testing.T) (*Engine, *mocks.MockTelegramClient, *mocks.MockLLMClient, *mocks.MockDBClient, func()) {
	ctrl := gomock.NewController(t)
	
	tgMock := mocks.NewMockTelegramClient(ctrl)
	twMock := mocks.NewMockTwilioClient(ctrl)
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
		tgMock,
		twMock,
		oauthCfg,
		gmailMock,
		tasksMock,
		nil, // contactsMock
		tavilyMock,
		audioMock,
		eventPublisherMock,
		"UTC",
	)

	dbMock.EXPECT().GetUser(gomock.Any()).Return(&models.User{PlatformID: "user-1", Name: "User One"}, nil).AnyTimes()

	cleanup := func() {
		engine.Wait()
		store.Close()
		mr.Close()
		ctrl.Finish()
	}

	return engine, tgMock, llmMock, dbMock, cleanup
}

func TestProcessMessage_ConnectCommand(t *testing.T) {
	engine, tgMock, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	msg := models.Message{
		ID:        "msg-1",
		UserID:    "user-1",
		ChatID:    "chat-1",
		Text:      "/connect",
		Source:    "telegram",
		Timestamp: time.Now(),
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

	msg := models.Message{
		ID:        "msg-2",
		UserID:    "user-1",
		ChatID:    "chat-1",
		Text:      "Hello, bot!",
		Source:    "telegram",
		Timestamp: time.Now(),
	}

	// Mock DB calls for history and memories
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.Memory{}, nil).AnyTimes()
	
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

	twMock := engine.twClient.(*mocks.MockTwilioClient)

	msg := models.Message{
		ID:               "wa-msg-1",
		UserID:           "+1234567890",
		ChatID:           "+1234567890",
		Text:             "Please setup my daily checklist:",
		Source:           "whatsapp",
		MediaURL:         "https://api.twilio.com/media/doc123",
		MediaContentType: "text/plain",
		Timestamp:        time.Now(),
	}

	docContent := "Habit 1: Drink water\nHabit 2: Stretch"
	twMock.EXPECT().DownloadMedia(gomock.Any(), "https://api.twilio.com/media/doc123").Return([]byte(docContent), nil).Times(1)

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.Memory{}, nil).AnyTimes()

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

	twMock := engine.twClient.(*mocks.MockTwilioClient)

	msg := models.Message{
		ID:               "wa-msg-2",
		UserID:           "+1234567890",
		ChatID:           "+1234567890",
		Text:             "",
		Source:           "whatsapp",
		MediaURL:         "https://api.twilio.com/media/pdf123",
		MediaContentType: "application/pdf",
		Timestamp:        time.Now(),
	}

	pdfBytes := []byte("%PDF-1.4 mock content")
	twMock.EXPECT().DownloadMedia(gomock.Any(), "https://api.twilio.com/media/pdf123").Return(pdfBytes, nil).Times(1)

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.Memory{}, nil).AnyTimes()

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

	twMock := engine.twClient.(*mocks.MockTwilioClient)

	msg := models.Message{
		ID:               "wa-msg-3",
		UserID:           "+1234567890",
		ChatID:           "+1234567890",
		Text:             "",
		Source:           "whatsapp",
		MediaURL:         "https://api.twilio.com/media/vid123",
		MediaContentType: "video/mp4",
		Timestamp:        time.Now(),
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

	twMock := engine.twClient.(*mocks.MockTwilioClient)

	msg := models.Message{
		ID:        "wa-blocked-1",
		UserID:    "+1234567890",
		ChatID:    "+1234567890",
		Text:      "תזכיר לי ב18 לנקות את השואב",
		Source:    "whatsapp",
		Timestamp: time.Now(),
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.Memory{}, nil).AnyTimes()

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

	twMock := engine.twClient.(*mocks.MockTwilioClient)

	msg := models.Message{
		ID:        "wa-blocked-2",
		UserID:    "+1234567890",
		ChatID:    "+1234567890",
		Text:      "some blocked text without numbers",
		Source:    "whatsapp",
		Timestamp: time.Now(),
	}

	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchMemories(gomock.Any(), gomock.Any(), gomock.Any()).Return([]models.Memory{}, nil).AnyTimes()

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

	dbMock.EXPECT().GetUser("user-1").Return(&models.User{PlatformID: "user-1", Name: "User One"}, nil).AnyTimes()

	msg := models.Message{UserID: "user-1", Text: "run custom tool"}
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

func setupUnauthTestEngine(t *testing.T) (*Engine, *mocks.MockTelegramClient, *mocks.MockDBClient, func()) {
	ctrl := gomock.NewController(t)

	tgMock := mocks.NewMockTelegramClient(ctrl)
	twMock := mocks.NewMockTwilioClient(ctrl)
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
		tgMock,
		twMock,
		oauthCfg,
		gmailMock,
		tasksMock,
		nil,
		tavilyMock,
		audioMock,
		eventPublisherMock,
		"UTC",
	)

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
	dbMock.EXPECT().GetUser("unauth-user").Return(nil, assert.AnError).AnyTimes()
	dbMock.EXPECT().GetPairingCode("000000").Return(nil, assert.AnError).AnyTimes()

	// 1-4 failures
	for i := 1; i <= 4; i++ {
		expectedRemaining := 5 - i
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			str, ok := x.(string)
			return ok && strings.Contains(str, fmt.Sprintf("%d attempts remaining", expectedRemaining))
		})).Return(nil).Times(1)

		msg := models.Message{UserID: "unauth-user", Text: "000000", Source: "telegram"}
		err := engine.ProcessMessage(context.Background(), msg)
		assert.NoError(t, err)
	}

	// 5th failure triggers lockout message
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "locked for 1 hour")
	})).Return(nil).Times(1)

	msg := models.Message{UserID: "unauth-user", Text: "000000", Source: "telegram"}
	err := engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)

	// 6th message while locked out returns temporarily locked out
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "temporarily locked out")
	})).Return(nil).Times(1)

	msg = models.Message{UserID: "unauth-user", Text: "any text", Source: "telegram"}
	err = engine.ProcessMessage(context.Background(), msg)
	assert.NoError(t, err)
}


