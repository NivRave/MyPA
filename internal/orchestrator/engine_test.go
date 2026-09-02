package orchestrator

import (
	"context"
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
		map[string]models.User{},
	)

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


	err := engine.processMessage(context.Background(), msg)
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

	err := engine.processMessage(context.Background(), msg)
	assert.NoError(t, err)
}
