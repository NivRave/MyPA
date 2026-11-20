package workspace_tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Option configures the workspace_tasks Module.
type Option func(*Module)

// WithClient sets a static TasksClient.
func WithClient(client orchestrator.TasksClient) Option {
	return func(m *Module) {
		m.client = client
	}
}

// WithClientProvider sets a dynamic TasksClient provider function.
func WithClientProvider(provider func() orchestrator.TasksClient) Option {
	return func(m *Module) {
		m.clientProvider = provider
	}
}

// WithEventBus sets the event bus.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for Google Tasks operations.
type Module struct {
	client         orchestrator.TasksClient
	clientProvider func() orchestrator.TasksClient
	bus            *events.EventBus
}

// NewModule initializes the workspace_tasks module.
func NewModule(opts ...Option) *Module {
	m := &Module{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Module) Name() string {
	return "workspace_tasks"
}

func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return ""
}

func (m *Module) getClient() orchestrator.TasksClient {
	if m.clientProvider != nil {
		return m.clientProvider()
	}
	return m.client
}

func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "list_task_lists",
			Description: "Lists all of the user's available task lists (like 'Groceries', 'Default', etc.) and their IDs.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "create_task_list",
			Description: "Creates a new task list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The title of the new task list.",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "list_tasks",
			Description: "Lists the user's pending Google Tasks from a specific task list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list to fetch tasks from. If omitted, fetches from the default list.",
					},
				},
			},
		},
		{
			Name:        "create_task",
			Description: "Creates a new task on a specific Google Tasks list.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list to create the task in. If omitted, uses the default list.",
					},
					"title": {
						Type:        genai.TypeString,
						Description: "The title of the task.",
					},
					"notes": {
						Type:        genai.TypeString,
						Description: "Optional additional notes for the task.",
					},
					"due": {
						Type:        genai.TypeString,
						Description: "Optional due date for the task in RFC 3339 format (e.g., '2023-10-01T00:00:00Z').",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "complete_task",
			Description: "Marks a task as completed in Google Tasks.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list containing the task. If omitted, uses the default list.",
					},
					"task_id": {
						Type:        genai.TypeString,
						Description: "The ID of the task to complete.",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "delete_task",
			Description: "Deletes a task entirely from Google Tasks.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"list_id": {
						Type:        genai.TypeString,
						Description: "Optional. The ID of the task list containing the task. If omitted, uses the default list.",
					},
					"task_id": {
						Type:        genai.TypeString,
						Description: "The ID of the task to delete.",
					},
				},
				Required: []string{"task_id"},
			},
		},
	}
}

func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	client := m.getClient()
	if client == nil {
		return "⚠️ Google Tasks integration is not configured.", nil
	}

	actorID := callCtx.ActorID()
	bus := m.bus
	if bus == nil {
		bus = callCtx.EventBus
	}

	switch toolName {
	case "list_task_lists":
		slog.Info("executing list_task_lists tool", "user", actorID)
		lists, err := client.ListTaskLists(ctx, actorID)
		if err != nil {
			slog.Error("list_task_lists failed", "error", err)
			return fmt.Sprintf("Error listing task lists: %v", err), nil
		}
		if len(lists) == 0 {
			return "You have no task lists.", nil
		}
		listsJSON, _ := json.Marshal(lists)
		return fmt.Sprintf("Here are the available task lists:\n%s", string(listsJSON)), nil

	case "create_task_list":
		title := toolutil.String(args, "title")
		if title == "" {
			return "Missing title parameter", nil
		}
		slog.Info("executing create_task_list tool", "user", actorID, "title", title)
		created, err := client.CreateTaskList(ctx, actorID, title)
		if err != nil {
			slog.Error("create_task_list failed", "error", err)
			return fmt.Sprintf("Error creating task list: %v", err), nil
		}

		listID := title
		if created != nil && created.Id != "" {
			listID = created.Id
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "tasks",
			Action:      "create",
			EntityType:  "TaskList",
			EntityID:    listID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Created task list '%s'", title),
		})

		return "Task list created successfully.", nil

	case "list_tasks":
		listID := toolutil.String(args, "list_id")
		slog.Info("executing list_tasks tool", "user", actorID, "list_id", listID)
		tasks, err := client.ListTasks(ctx, actorID, listID)
		if err != nil {
			slog.Error("list_tasks failed", "error", err)
			return fmt.Sprintf("Error listing tasks: %v", err), nil
		}
		if len(tasks) == 0 {
			return "You have no tasks on this list.", nil
		}
		tasksJSON, _ := json.Marshal(tasks)
		return fmt.Sprintf("Here are the tasks in this list:\n%s", string(tasksJSON)), nil

	case "create_task":
		listID := toolutil.String(args, "list_id")
		title := toolutil.String(args, "title")
		notes := toolutil.String(args, "notes")
		due := toolutil.String(args, "due")

		if title == "" {
			return "Missing title parameter", nil
		}

		slog.Info("executing create_task tool", "user", actorID, "title", title)
		err := client.CreateTask(ctx, actorID, listID, title, notes, due)
		if err != nil {
			slog.Error("create_task failed", "error", err)
			return fmt.Sprintf("Error creating task: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "tasks",
			Action:      "create",
			EntityType:  "Task",
			EntityID:    title,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Created task '%s'", title),
		})

		return "Task created successfully.", nil

	case "complete_task":
		listID := toolutil.String(args, "list_id")
		taskID := toolutil.String(args, "task_id")
		if taskID == "" {
			return "Missing task_id parameter", nil
		}

		slog.Info("executing complete_task tool", "user", actorID, "task_id", taskID)
		err := client.CompleteTask(ctx, actorID, listID, taskID)
		if err != nil {
			slog.Error("complete_task failed", "error", err)
			return fmt.Sprintf("Error completing task: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "tasks",
			Action:      "complete",
			EntityType:  "Task",
			EntityID:    taskID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Completed task '%s'", taskID),
		})

		return "Task marked as completed.", nil

	case "delete_task":
		listID := toolutil.String(args, "list_id")
		taskID := toolutil.String(args, "task_id")
		if taskID == "" {
			return "Missing task_id parameter", nil
		}

		slog.Info("executing delete_task tool", "user", actorID, "task_id", taskID)
		err := client.DeleteTask(ctx, actorID, listID, taskID)
		if err != nil {
			slog.Error("delete_task failed", "error", err)
			return fmt.Sprintf("Error deleting task: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "tasks",
			Action:      "delete",
			EntityType:  "Task",
			EntityID:    taskID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Deleted task '%s'", taskID),
		})

		return "Task deleted successfully.", nil

	default:
		return "", fmt.Errorf("unknown tool %s in workspace_tasks", toolName)
	}
}
