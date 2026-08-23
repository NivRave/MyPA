package tasks

import (
	"context"
	"fmt"

	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/state"
	tasksapi "google.golang.org/api/tasks/v1"
	"google.golang.org/api/option"
)

// Client is a wrapper around the Google Tasks API.
type Client struct {
	oauthConfig *calendar.OAuthConfig
	store       *state.Store
}

// NewClient initializes a new Tasks client wrapper.
func NewClient(oauthCfg *calendar.OAuthConfig, store *state.Store) *Client {
	return &Client{
		oauthConfig: oauthCfg,
		store:       store,
	}
}

func (c *Client) getService(ctx context.Context, userID string) (*tasksapi.Service, error) {
	tokenData, err := c.store.GetOAuthToken(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("no token found for user %s: %w", userID, err)
	}

	token, err := calendar.DecodeToken(tokenData)
	if err != nil {
		return nil, err
	}

	ts := c.oauthConfig.TokenSource(ctx, token)
	srv, err := tasksapi.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("failed to create tasks client: %w", err)
	}

	return srv, nil
}

// ListTaskLists fetches all the user's task lists.
func (c *Client) ListTaskLists(ctx context.Context, userID string) ([]*tasksapi.TaskList, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	res, err := srv.Tasklists.List().Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list task lists: %w", err)
	}

	return res.Items, nil
}

// CreateTaskList creates a new task list.
func (c *Client) CreateTaskList(ctx context.Context, userID, title string) (*tasksapi.TaskList, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	taskList := &tasksapi.TaskList{
		Title: title,
	}

	res, err := srv.Tasklists.Insert(taskList).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create task list: %w", err)
	}
	return res, nil
}

// ListTasks fetches the user's tasks from the specified list (defaults to "@default").
func (c *Client) ListTasks(ctx context.Context, userID, listID string) ([]*tasksapi.Task, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	if listID == "" {
		listID = "@default"
	}

	// Fetch tasks from the specified list
	// ShowHidden = true to optionally see completed if desired, but default is false
	res, err := srv.Tasks.List(listID).ShowHidden(false).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return res.Items, nil
}

// CreateTask adds a new task to the specified list.
func (c *Client) CreateTask(ctx context.Context, userID, listID, title, notes, due string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	task := &tasksapi.Task{
		Title: title,
		Notes: notes,
	}
	
	if due != "" {
		// API expects RFC 3339 timestamp formatted string
		task.Due = due
	}

	if listID == "" {
		listID = "@default"
	}

	_, err = srv.Tasks.Insert(listID, task).Do()
	return err
}

// CompleteTask marks a task as completed in the specified list.
func (c *Client) CompleteTask(ctx context.Context, userID, listID, taskID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	if listID == "" {
		listID = "@default"
	}

	task, err := srv.Tasks.Get(listID, taskID).Do()
	if err != nil {
		return fmt.Errorf("failed to fetch task to complete: %w", err)
	}

	task.Status = "completed"
	_, err = srv.Tasks.Update(listID, task.Id, task).Do()
	return err
}

// DeleteTask removes a task entirely from the specified list.
func (c *Client) DeleteTask(ctx context.Context, userID, listID, taskID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	if listID == "" {
		listID = "@default"
	}

	err = srv.Tasks.Delete(listID, taskID).Do()
	return err
}
