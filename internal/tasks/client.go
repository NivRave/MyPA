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

// ListTasks fetches the user's tasks from the default list.
func (c *Client) ListTasks(ctx context.Context, userID string) ([]*tasksapi.Task, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Fetch tasks from the "@default" list
	// ShowHidden = true to optionally see completed if desired, but default is false
	res, err := srv.Tasks.List("@default").ShowHidden(false).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return res.Items, nil
}

// CreateTask adds a new task to the default list.
func (c *Client) CreateTask(ctx context.Context, userID, title, notes, due string) error {
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

	_, err = srv.Tasks.Insert("@default", task).Do()
	return err
}

// CompleteTask marks a task as completed.
func (c *Client) CompleteTask(ctx context.Context, userID, taskID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	task, err := srv.Tasks.Get("@default", taskID).Do()
	if err != nil {
		return fmt.Errorf("failed to fetch task to complete: %w", err)
	}

	task.Status = "completed"
	_, err = srv.Tasks.Update("@default", task.Id, task).Do()
	return err
}

// DeleteTask removes a task entirely from the list.
func (c *Client) DeleteTask(ctx context.Context, userID, taskID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	err = srv.Tasks.Delete("@default", taskID).Do()
	return err
}
