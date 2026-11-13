package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*db.Client, func()) {
	return testutil.NewTestDB(t)
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

	tempDir := t.TempDir()

	// Run archiver targeting isolated tempDir
	err := archiveOldSessions(client, tempDir)
	require.NoError(t, err)

	// Verify old session is gone
	var checkOld models.AuditSession
	res := client.DB.Where("id = ?", oldSession.ID).First(&checkOld)
	assert.Error(t, res.Error, "Old session should be deleted")

	// Verify new session is still there
	var checkNew models.AuditSession
	res = client.DB.Where("id = ?", newSession.ID).First(&checkNew)
	assert.NoError(t, res.Error, "New session should not be deleted")

	// Verify archive file was created in tempDir
	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	var archiveFilePath string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "audit_archive_") {
			archiveFilePath = filepath.Join(tempDir, f.Name())
			break
		}
	}
	require.NotEmpty(t, archiveFilePath, "Archive file should be created in tempDir")

	// Verify content of the file
	file, err := os.Open(archiveFilePath)
	require.NoError(t, err)
	defer file.Close()

	b, _ := io.ReadAll(file)
	assert.Contains(t, string(b), "Old prompt")
	assert.NotContains(t, string(b), "New prompt")
}
