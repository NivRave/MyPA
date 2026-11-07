package workspace_tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	tasksapi "google.golang.org/api/tasks/v1"
)

type mockTasksClient struct {
	createdList  string
	createdTitle string
	completedID  string
	deletedID    string
}

func (m *mockTasksClient) ListTaskLists(ctx context.Context, userID string) ([]*tasksapi.TaskList, error) {
	return []*tasksapi.TaskList{
		{Id: "list_1", Title: "Groceries"},
	}, nil
}

func (m *mockTasksClient) CreateTaskList(ctx context.Context, userID string, title string) (*tasksapi.TaskList, error) {
	m.createdList = title
	return &tasksapi.TaskList{Id: "list_new", Title: title}, nil
}

func (m *mockTasksClient) ListTasks(ctx context.Context, userID string, listID string) ([]*tasksapi.Task, error) {
	return []*tasksapi.Task{
		{Id: "task_1", Title: "Buy apples"},
	}, nil
}

func (m *mockTasksClient) CreateTask(ctx context.Context, userID string, listID string, title, notes, due string) error {
	m.createdTitle = title
	return nil
}

func (m *mockTasksClient) CompleteTask(ctx context.Context, userID string, listID string, taskID string) error {
	m.completedID = taskID
	return nil
}

func (m *mockTasksClient) DeleteTask(ctx context.Context, userID string, listID string, taskID string) error {
	m.deletedID = taskID
	return nil
}

func TestWorkspaceTasks_Operations(t *testing.T) {
	mockClient := &mockTasksClient{}
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 10)
	bus.SubscribeTopic("tasks", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(WithClient(mockClient), WithEventBus(bus))
	if mod.Name() != "workspace_tasks" {
		t.Errorf("expected name workspace_tasks, got %s", mod.Name())
	}
	if len(mod.Declarations()) != 6 {
		t.Errorf("expected 6 declarations, got %d", len(mod.Declarations()))
	}

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_789",
			FamilyGroup: "family_tasks",
		},
		EventBus: bus,
	}

	// 1. List Task Lists
	res, err := mod.Execute(context.Background(), callCtx, "list_task_lists", nil)
	if err != nil || !strings.Contains(res, "Groceries") {
		t.Fatalf("list_task_lists failed: res=%s, err=%v", res, err)
	}

	// 2. Create Task List
	res, err = mod.Execute(context.Background(), callCtx, "create_task_list", map[string]interface{}{
		"title": "Hardware",
	})
	if err != nil || !strings.Contains(res, "Task list created successfully") {
		t.Fatalf("create_task_list failed: res=%s, err=%v", res, err)
	}
	if mockClient.createdList != "Hardware" {
		t.Errorf("expected list 'Hardware', got %s", mockClient.createdList)
	}

	// 3. List Tasks
	res, err = mod.Execute(context.Background(), callCtx, "list_tasks", map[string]interface{}{
		"list_id": "list_1",
	})
	if err != nil || !strings.Contains(res, "Buy apples") {
		t.Fatalf("list_tasks failed: res=%s, err=%v", res, err)
	}

	// 4. Create Task
	res, err = mod.Execute(context.Background(), callCtx, "create_task", map[string]interface{}{
		"title": "Buy milk",
		"notes": "2% fat",
	})
	if err != nil || !strings.Contains(res, "Task created successfully") {
		t.Fatalf("create_task failed: res=%s, err=%v", res, err)
	}
	if mockClient.createdTitle != "Buy milk" {
		t.Errorf("expected task title 'Buy milk', got %s", mockClient.createdTitle)
	}

	// 5. Complete Task
	res, err = mod.Execute(context.Background(), callCtx, "complete_task", map[string]interface{}{
		"task_id": "task_1",
	})
	if err != nil || !strings.Contains(res, "Task marked as completed") {
		t.Fatalf("complete_task failed: res=%s, err=%v", res, err)
	}
	if mockClient.completedID != "task_1" {
		t.Errorf("expected completed task_1, got %s", mockClient.completedID)
	}

	// 6. Delete Task
	res, err = mod.Execute(context.Background(), callCtx, "delete_task", map[string]interface{}{
		"task_id": "task_1",
	})
	if err != nil || !strings.Contains(res, "Task deleted successfully") {
		t.Fatalf("delete_task failed: res=%s, err=%v", res, err)
	}
	if mockClient.deletedID != "task_1" {
		t.Errorf("expected deleted task_1, got %s", mockClient.deletedID)
	}

	if len(receivedEvents) == 0 {
		t.Error("expected mutation events on tasks topic")
	}
}
