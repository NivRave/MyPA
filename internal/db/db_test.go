package db

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testDBClient *Client

func setupTestDB(t *testing.T) (*Client, func()) {
	ctx := context.Background()

	dbName := "testdb"
	dbUser := "user"
	dbPassword := "password"

	postgresContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("pgvector/pgvector:pg16"),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	client, err := NewClient(connStr)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	cleanup := func() {
		if err := postgresContainer.Terminate(ctx); err != nil {
			log.Fatalf("failed to terminate container: %s", err)
		}
	}

	return client, cleanup
}

func TestNewClient_EmptyDSN(t *testing.T) {
	_, err := NewClient("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database URL is empty")
}

func TestAuditSessions(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	userID := "user-123"

	session1 := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        userID,
		StartTime:     time.Now().Add(-1 * time.Hour),
		EndTime:       time.Now(),
		UserPrompt:    "Prompt 1",
		FinalResponse: "Response 1",
	}
	err := client.InsertAuditSession(session1)
	require.NoError(t, err)

	session2 := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        userID,
		StartTime:     time.Now(),
		EndTime:       time.Now().Add(1 * time.Hour),
		UserPrompt:    "Prompt 2",
		FinalResponse: "Response 2",
	}
	err = client.InsertAuditSession(session2)
	require.NoError(t, err)

	last, err := client.GetLastAuditSessionForUser(userID)
	require.NoError(t, err)
	assert.Equal(t, "Prompt 2", last.UserPrompt)

	users, err := client.GetUniqueUsers()
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, userID, users[0])
}

func TestAuditEvents(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	event := models.AuditEvent{
		ID:              uuid.New().String(),
		SessionID:       uuid.New().String(),
		EventType:       "TEST_EVENT",
		ActionName:      "test action",
		RequestPayload:  "{}",
		ResponsePayload: "{}",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Second),
	}

	err := client.InsertAuditEvent(event)
	require.NoError(t, err)
}

func TestMemories(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	userID := "user-456"

	vec1 := make([]float32, 3072)
	vec1[0] = 1.0
	
	mem1 := models.Memory{
		UserID:    userID,
		Fact:      "I love apples",
		Embedding: pgvector.NewVector(vec1),
		CreatedAt: time.Now(),
	}
	err := client.SaveMemory(mem1)
	require.NoError(t, err)

	vec2 := make([]float32, 3072)
	vec2[1] = 1.0

	mem2 := models.Memory{
		UserID:    userID,
		Fact:      "I love bananas",
		Embedding: pgvector.NewVector(vec2),
		CreatedAt: time.Now(),
	}
	err = client.SaveMemory(mem2)
	require.NoError(t, err)

	searchVec := make([]float32, 3072)
	searchVec[0] = 0.9

	results, err := client.SearchMemories(userID, pgvector.NewVector(searchVec), 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "I love apples", results[0].Fact)
}

func TestReminders(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	rem1 := models.ScheduledReminder{
		UserID:  "user-rem",
		Message: "Past reminder",
		DueTime: time.Now().Add(-1 * time.Hour),
		IsSent:  false,
	}
	err := client.SaveReminder(rem1)
	require.NoError(t, err)

	rem2 := models.ScheduledReminder{
		UserID:  "user-rem",
		Message: "Future reminder",
		DueTime: time.Now().Add(1 * time.Hour),
		IsSent:  false,
	}
	err = client.SaveReminder(rem2)
	require.NoError(t, err)

	due, err := client.GetDueReminders()
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "Past reminder", due[0].Message)

	err = client.MarkReminderSent(due[0].ID)
	require.NoError(t, err)

	dueAgain, err := client.GetDueReminders()
	require.NoError(t, err)
	assert.Empty(t, dueAgain)
}
