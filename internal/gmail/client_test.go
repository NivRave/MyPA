package gmail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTest(t *testing.T) (*Client, *httptest.Server, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)

	ctx := context.Background()
	userID := "testuser"
	
	validToken := []byte(`{"access_token":"mock-token","token_type":"Bearer","refresh_token":"mock-refresh","expiry":"2030-01-01T00:00:00Z"}`)
	err = store.SetOAuthToken(ctx, userID, validToken)
	require.NoError(t, err)

	oauthCfg := &calendar.OAuthConfig{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			// SearchEmails
			w.Write([]byte(`{
				"messages": [
					{"id": "msg1", "threadId": "thread1"}
				],
				"resultSizeEstimate": 1
			}`))
		case "/gmail/v1/users/me/messages/msg1":
			// Get Message Details
			if r.URL.Query().Get("format") == "metadata" {
				w.Write([]byte(`{
					"id": "msg1",
					"snippet": "Test snippet",
					"payload": {
						"headers": [
							{"name": "From", "value": "sender@test.com"},
							{"name": "Subject", "value": "Test Subject"}
						]
					}
				}`))
			} else if r.URL.Query().Get("format") == "full" {
				// ReadEmail
				w.Write([]byte(`{
					"id": "msg1",
					"payload": {
						"mimeType": "text/plain",
						"body": {
							"data": "SGVsbG8gV29ybGQ=" 
						}
					}
				}`))
			}
		case "/gmail/v1/users/me/drafts":
			// DraftReply
			w.Write([]byte(`{"id": "draft1"}`))
		case "/gmail/v1/users/me/messages/msg1/modify":
			// ArchiveEmail / ApplyLabel
			w.Write([]byte(`{"id": "msg1"}`))
		case "/gmail/v1/users/me/messages/msg1/trash":
			// SoftDeleteEmail
			w.Write([]byte(`{"id": "msg1"}`))
		case "/gmail/v1/users/me/labels":
			// ListLabels / CreateLabel
			if r.Method == http.MethodPost {
				w.Write([]byte(`{"id": "label2", "name": "NEW_LABEL"}`))
			} else {
				w.Write([]byte(`{
					"labels": [
						{"id": "label1", "name": "TEST_LABEL"}
					]
				}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	client := NewClient(oauthCfg, store)
	client.endpoint = ts.URL

	return client, ts, mr
}

func TestClient_SearchEmails(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	emails, err := client.SearchEmails(context.Background(), "testuser", "is:unread", 10)
	require.NoError(t, err)
	require.Len(t, emails, 1)

	assert.Equal(t, "msg1", emails[0].ID)
	assert.Equal(t, "sender@test.com", emails[0].From)
	assert.Equal(t, "Test Subject", emails[0].Subject)
	assert.Equal(t, "Test snippet", emails[0].Snippet)
}

func TestClient_ReadEmail(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	body, err := client.ReadEmail(context.Background(), "testuser", "msg1")
	require.NoError(t, err)
	assert.Equal(t, "Hello World", body)
}

func TestClient_DraftReply(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	err := client.DraftReply(context.Background(), "testuser", "msg1", "This is a reply")
	require.NoError(t, err)
}

func TestClient_ArchiveEmail(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	err := client.ArchiveEmail(context.Background(), "testuser", "msg1")
	require.NoError(t, err)
}

func TestClient_SoftDeleteEmail(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	err := client.SoftDeleteEmail(context.Background(), "testuser", "msg1")
	require.NoError(t, err)
}

func TestClient_ListLabels(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	labels, err := client.ListLabels(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, "label1", labels["TEST_LABEL"])
}

func TestClient_CreateLabel(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	id, err := client.CreateLabel(context.Background(), "testuser", "NEW_LABEL")
	require.NoError(t, err)
	assert.Equal(t, "label2", id)
}

func TestClient_ApplyLabel(t *testing.T) {
	client, ts, mr := setupTest(t)
	defer ts.Close()
	defer mr.Close()

	err := client.ApplyLabel(context.Background(), "testuser", "msg1", "label1")
	require.NoError(t, err)
}
