package workspace_tasks

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tasksapi "google.golang.org/api/tasks/v1"
)

type recordingTasksClient struct {
	lastListUser       string
	lastCreateListUser string
	lastListTasksUser  string
	lastCreateTaskUser string
	lastCompleteUser   string
	lastDeleteUser     string
}

func (r *recordingTasksClient) ListTaskLists(ctx context.Context, userID string) ([]*tasksapi.TaskList, error) {
	r.lastListUser = userID
	return []*tasksapi.TaskList{{Id: "list_1", Title: "Default"}}, nil
}
func (r *recordingTasksClient) CreateTaskList(ctx context.Context, userID string, title string) (*tasksapi.TaskList, error) {
	r.lastCreateListUser = userID
	return &tasksapi.TaskList{Id: "list_2", Title: title}, nil
}
func (r *recordingTasksClient) ListTasks(ctx context.Context, userID string, listID string) ([]*tasksapi.Task, error) {
	r.lastListTasksUser = userID
	return []*tasksapi.Task{{Id: "task_1", Title: "Task"}}, nil
}
func (r *recordingTasksClient) CreateTask(ctx context.Context, userID string, listID string, title, notes, due string) error {
	r.lastCreateTaskUser = userID
	return nil
}
func (r *recordingTasksClient) CompleteTask(ctx context.Context, userID string, listID string, taskID string) error {
	r.lastCompleteUser = userID
	return nil
}
func (r *recordingTasksClient) DeleteTask(ctx context.Context, userID string, listID string, taskID string) error {
	r.lastDeleteUser = userID
	return nil
}

// TestWorkspaceTasks_AntiSpoofing verifies that workspace_tasks strictly enforces
// canonical identity (callCtx.ActorID() and callCtx.TenantFamilyGroup()) and ignores spoofed args.
func TestWorkspaceTasks_AntiSpoofing(t *testing.T) {
	client := &recordingTasksClient{}
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 20)
	bus.SubscribeTopic("tasks", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := NewModule(WithClient(client), WithEventBus(bus))

	// Canonical user identity
	legitUser := &models.User{
		ID:          "uuid-victim",
		PlatformID:  "canonical_victim_123",
		FamilyGroup: "family_legit",
	}
	callCtx := orchestrator.CallContext{
		User:     legitUser,
		Scope:    identitytest.Scope(t, "family_legit", "canonical_victim_123", "family"), // V4-30: the tenant
		Message:  channel.Inbound{Sender: "canonical_victim_123"},
		EventBus: bus,
	}

	adversarialArgs := map[string]interface{}{
		"user_id":       "spoofed_attacker_999",
		"family_group":  "evil_family",
		"actor_user_id": "spoofed_attacker_999",
		"title":         "Spoof Task",
		"list_id":       "list_1",
		"task_id":       "task_1",
	}

	t.Run("list_task_lists ignores spoofed user_id", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "list_task_lists", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastListUser, "Client MUST receive canonical ActorID()")
		assert.NotEqual(t, "spoofed_attacker_999", client.lastListUser)
	})

	t.Run("create_task_list enforces canonical ActorID and TenantFamilyGroup in event", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "create_task_list", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastCreateListUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "canonical_victim_123", ev.ActorUserID)
			assert.Equal(t, "family_legit", ev.FamilyGroup)
			assert.NotEqual(t, "spoofed_attacker_999", ev.ActorUserID)
			assert.NotEqual(t, "evil_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for create_task_list")
		}
	})

	t.Run("list_tasks ignores spoofed user_id", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "list_tasks", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastListTasksUser)
	})

	t.Run("create_task enforces canonical ActorID and TenantFamilyGroup in event", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "create_task", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastCreateTaskUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "canonical_victim_123", ev.ActorUserID)
			assert.Equal(t, "family_legit", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for create_task")
		}
	})

	t.Run("complete_task enforces canonical ActorID and TenantFamilyGroup in event", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "complete_task", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastCompleteUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "canonical_victim_123", ev.ActorUserID)
			assert.Equal(t, "family_legit", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for complete_task")
		}
	})

	t.Run("delete_task enforces canonical ActorID and TenantFamilyGroup in event", func(t *testing.T) {
		_, err := mod.Execute(context.Background(), callCtx, "delete_task", adversarialArgs)
		require.NoError(t, err)
		assert.Equal(t, "canonical_victim_123", client.lastDeleteUser)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "canonical_victim_123", ev.ActorUserID)
			assert.Equal(t, "family_legit", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for delete_task")
		}
	})
}
