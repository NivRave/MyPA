package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/genai"
)

func TestEngine_HandleToolCall_CalendarCreate(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	calMock := mocks.NewMockCalendarClient(ctrl)
	engine.calendarFactory = func(ctx context.Context, userID string) (CalendarClient, error) {
		return calMock, nil
	}

	msg := models.Message{UserID: "user1", Text: "create event"}
	toolCall := &genai.FunctionCall{
		Name: "create_calendar_event",
		Args: map[string]any{
			"title": "Meeting",
			"start_time": "2030-01-01T10:00:00Z",
		},
	}

	calMock.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return("http://link", nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Event created!"}, nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Event created!", res)
}

func TestEngine_HandleToolCall_TasksCreate(t *testing.T) {
	engine, _, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// tasksMock is already injected via setupTestEngine. We need to cast it or grab it.
	// Actually, setupTestEngine doesn't return tasksMock. Let's write a helper to just override it.
	tasksMock := mocks.NewMockTasksClient(ctrl)
	engine.tasksClient = tasksMock

	msg := models.Message{UserID: "user1", Text: "create task"}
	toolCall := &genai.FunctionCall{
		Name: "create_task",
		Args: map[string]any{
			"list_id": "list1",
			"title": "Buy milk",
		},
	}

	tasksMock.EXPECT().CreateTask(gomock.Any(), "user1", "list1", "Buy milk", gomock.Any(), gomock.Any()).Return(nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "Task created!"}, nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "Task created!", res)
}

func TestEngine_HandleToolCall_Tavily(t *testing.T) {
	engine, tgMock, llmMock, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tavilyMock := mocks.NewMockTavilyClient(ctrl)
	engine.tavilyClient = tavilyMock

	msg := models.Message{UserID: "user1", Text: "search web"}
	toolCall := &genai.FunctionCall{
		Name: "search_web",
		Args: map[string]any{
			"query": "golang",
		},
	}

	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tavilyMock.EXPECT().Search(gomock.Any(), "golang").Return("golang is great", nil)
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "I found this: golang is great"}, nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "I found this: golang is great", res)
}

func TestEngine_HandleToolCall_GmailSearch(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	gmailMock := mocks.NewMockGmailClient(ctrl)
	engine.gmailClient = gmailMock

	msg := models.Message{UserID: "user1", Text: "check emails"}
	toolCall := &genai.FunctionCall{
		Name: "search_emails",
		Args: map[string]any{
			"query": "is:unread",
		},
	}

	gmailMock.EXPECT().SearchEmails(gomock.Any(), "user1", "is:unread", int64(5)).Return(nil, nil)
	// When length is 0, it doesn't call LLM again
	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "You have no unread emails.", res)
}

func TestEngine_HandleToolCall_ScheduleReminder(t *testing.T) {
	engine, _, _, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()

	msg := models.Message{UserID: "user1", Text: "remind me"}
	dueTime := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
	toolCall := &genai.FunctionCall{
		Name: "schedule_reminder",
		Args: map[string]any{
			"message": "Drink water",
			"due_time": dueTime,
		},
	}

	dbMock.EXPECT().SaveReminder(gomock.Any()).Return(nil)

	res, err := engine.handleToolCall(context.Background(), msg, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Contains(t, res, "Reminder Set!")
}

func TestEngine_HandleToolCall_Unknown(t *testing.T) {
	engine, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	toolCall := &genai.FunctionCall{
		Name: "unknown_tool",
	}

	res, err := engine.handleToolCall(context.Background(), models.Message{}, nil, "system", toolCall)
	require.NoError(t, err)
	assert.Equal(t, "⚠️ LLM tried to call an unknown tool.", res)
}
