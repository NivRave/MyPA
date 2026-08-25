package main

import (
	"context"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) (*db.Client, func()) {
	ctx := context.Background()

	dbName := "testdb"
	dbUser := "user"
	dbPassword := "password"

	postgresContainer, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
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

	client, err := db.NewClient(connStr)
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

func TestArchiveOldSessions(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert an old session (> 30 days)
	oldSession := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        "user-1",
		StartTime:     time.Now().Add(-31 * 24 * time.Hour),
		EndTime:       time.Now().Add(-31 * 24 * time.Hour),
		UserPrompt:    "Old prompt",
		FinalResponse: "Old response",
	}
	require.NoError(t, client.InsertAuditSession(oldSession))

	// Insert a new session (< 30 days)
	newSession := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        "user-2",
		StartTime:     time.Now().Add(-10 * 24 * time.Hour),
		EndTime:       time.Now().Add(-10 * 24 * time.Hour),
		UserPrompt:    "New prompt",
		FinalResponse: "New response",
	}
	require.NoError(t, client.InsertAuditSession(newSession))

	// Run archiver
	archiveOldSessions(client)

	// Verify old session is gone
	var checkOld models.AuditSession
	res := client.DB.Where("id = ?", oldSession.ID).First(&checkOld)
	assert.Error(t, res.Error, "Old session should be deleted")

	// Verify new session is still there
	var checkNew models.AuditSession
	res = client.DB.Where("id = ?", newSession.ID).First(&checkNew)
	assert.NoError(t, res.Error, "New session should not be deleted")

	// Check if file was created. We can use filepath.Glob("audit_archive_*.jsonl")
	// But it's easier to just find the newest file in the current directory starting with audit_archive_
	files, err := os.ReadDir(".")
	require.NoError(t, err)

	var archiveFile string
	for _, f := range files {
		if !f.IsDir() && len(f.Name()) > 14 && f.Name()[:14] == "audit_archive_" {
			archiveFile = f.Name()
			break
		}
	}
	require.NotEmpty(t, archiveFile, "Archive file should be created")

	// Verify content of the file
	file, err := os.Open(archiveFile)
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(archiveFile)

	b, _ := io.ReadAll(file)
	assert.Contains(t, string(b), "Old prompt")
	assert.NotContains(t, string(b), "New prompt")
}
