package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
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

func setupTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	ts := httptest.NewServer(handler)
	
	client, err := NewClient(context.Background(), &dummyTokenSource{}, option.WithEndpoint(ts.URL))
	require.NoError(t, err)

	return client, ts
}

func TestClient_CreateEvent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/calendars/primary/events")
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"htmlLink": "https://calendar.google.com/events/123"}`))
	}

	client, ts := setupTestClient(t, handler)
	defer ts.Close()

	ev := models.CalendarEvent{
		Title:       "Test Event",
		Description: "Desc",
		StartTime:   "2030-01-01T10:00:00Z",
		EndTime:     "2030-01-01T11:00:00Z",
		Timezone:    "UTC",
	}

	link, err := client.CreateEvent(context.Background(), ev)
	require.NoError(t, err)
	assert.Equal(t, "https://calendar.google.com/events/123", link)
}

func TestClient_ListEvents(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/calendars/primary/events")
		
		assert.Equal(t, "2030-01-01T00:00:00Z", r.URL.Query().Get("timeMin"))
		assert.Equal(t, "query", r.URL.Query().Get("q"))

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"items": [
				{
					"id": "event-1",
					"summary": "Meeting"
				}
			]
		}`))
	}

	client, ts := setupTestClient(t, handler)
	defer ts.Close()

	events, err := client.ListEvents(context.Background(), "2030-01-01T00:00:00Z", "", "query")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "event-1", events[0].Id)
	assert.Equal(t, "Meeting", events[0].Summary)
}

func TestClient_UpdateEvent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PATCH", r.Method)
		assert.Contains(t, r.URL.Path, "/calendars/primary/events/event-1")
		
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "event-1"}`))
	}

	client, ts := setupTestClient(t, handler)
	defer ts.Close()

	ev := models.CalendarEvent{
		Title: "Updated Title",
	}

	err := client.UpdateEvent(context.Background(), "event-1", ev)
	require.NoError(t, err)
}

func TestClient_DeleteEvent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		assert.Contains(t, r.URL.Path, "/calendars/primary/events/event-1")
		
		w.WriteHeader(http.StatusNoContent)
	}

	client, ts := setupTestClient(t, handler)
	defer ts.Close()

	err := client.DeleteEvent(context.Background(), "event-1")
	require.NoError(t, err)
}

func TestFormatDateTime(t *testing.T) {
	dt := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "2025-01-01T12:00:00Z", FormatDateTime(dt))
}
