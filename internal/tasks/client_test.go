package tasks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

type dummyTokenSource struct{}

func (t *dummyTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: "dummy-token",
		Expiry:      time.Now().Add(1 * time.Hour),
	}, nil
}

func setupTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server, func()) {
	ts := httptest.NewServer(handler)
	
	mr, err := miniredis.Run()
	require.NoError(t, err)
	
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)

	// Save dummy token for "user-123" so that getService succeeds
	tokenBytes := []byte(`{"access_token":"dummy-token","token_type":"Bearer","expiry":"2030-01-01T00:00:00Z"}`)
	err = store.SetOAuthToken(context.Background(), "user-123", tokenBytes)
	require.NoError(t, err)

	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	})

	client := NewClient(oauthCfg, store, option.WithEndpoint(ts.URL))
	
	cleanup := func() {
		ts.Close()
		store.Close()
		mr.Close()
	}

	return client, ts, cleanup
}

func TestClient_ListTaskLists(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/users/@me/lists")
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"items": [
				{
					"id": "list-1",
					"title": "My Tasks"
				}
			]
		}`))
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	lists, err := client.ListTaskLists(context.Background(), "user-123")
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, "list-1", lists[0].Id)
	assert.Equal(t, "My Tasks", lists[0].Title)
}

func TestClient_CreateTaskList(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/users/@me/lists")
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "new-list-1", "title": "New List"}`))
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	list, err := client.CreateTaskList(context.Background(), "user-123", "New List")
	require.NoError(t, err)
	assert.Equal(t, "new-list-1", list.Id)
	assert.Equal(t, "New List", list.Title)
}

func TestClient_ListTasks(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/lists/list-1/tasks")
		assert.Equal(t, "false", r.URL.Query().Get("showHidden"))
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"items": [
				{
					"id": "task-1",
					"title": "Buy Milk"
				}
			]
		}`))
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	tasks, err := client.ListTasks(context.Background(), "user-123", "list-1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-1", tasks[0].Id)
}

func TestClient_CreateTask(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/lists/@default/tasks") // testing empty listID defaults to @default
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "new-task-1"}`))
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	err := client.CreateTask(context.Background(), "user-123", "", "Buy Milk", "from store", "2030-01-01T00:00:00Z")
	require.NoError(t, err)
}

func TestClient_CompleteTask(t *testing.T) {
	// CompleteTask does a GET followed by a PUT (or PATCH).
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			assert.Contains(t, r.URL.Path, "/lists/list-1/tasks/task-1")
			w.Write([]byte(`{"id": "task-1", "title": "Buy Milk", "status": "needsAction"}`))
		} else if r.Method == "PUT" {
			assert.Contains(t, r.URL.Path, "/lists/list-1/tasks/task-1")
			w.Write([]byte(`{"id": "task-1", "title": "Buy Milk", "status": "completed"}`))
		}
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	err := client.CompleteTask(context.Background(), "user-123", "list-1", "task-1")
	require.NoError(t, err)
}

func TestClient_DeleteTask(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		assert.Contains(t, r.URL.Path, "/lists/list-1/tasks/task-1")
		
		w.WriteHeader(http.StatusNoContent)
	}

	client, _, cleanup := setupTestClient(t, handler)
	defer cleanup()

	err := client.DeleteTask(context.Background(), "user-123", "list-1", "task-1")
	require.NoError(t, err)
}

func TestClient_Unauthorized(t *testing.T) {
	client, _, cleanup := setupTestClient(t, nil)
	defer cleanup()

	// Should fail because user-456 has no token in the store
	_, err := client.ListTaskLists(context.Background(), "user-456")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no token found")
}
