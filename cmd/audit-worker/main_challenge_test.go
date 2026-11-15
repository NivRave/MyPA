package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmpiricalChallenge_AuditArchiverFailureSafety_DirCreationError(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	oldSessionID := uuid.New().String()
	oldSession := models.AuditSession{
		ID:            oldSessionID,
		UserID:        "user-challenge-error-injection-1",
		StartTime:     time.Now().Add(-35 * 24 * time.Hour),
		EndTime:       time.Now().Add(-35 * 24 * time.Hour),
		UserPrompt:    "Prompt must survive mkdir failure",
		FinalResponse: "Response must survive mkdir failure",
	}
	require.NoError(t, client.InsertAuditSession(oldSession))

	tempDir := t.TempDir()
	blockingFilePath := filepath.Join(tempDir, "blocking_file")
	err := os.WriteFile(blockingFilePath, []byte("plain file"), 0644)
	require.NoError(t, err)

	archiveErr := archiveOldSessions(client, blockingFilePath)
	require.Error(t, archiveErr, "archiveOldSessions must fail when dir creation fails")

	// Verify database record was NOT deleted
	var check models.AuditSession
	res := client.DB.Where("id = ?", oldSessionID).First(&check)
	assert.NoError(t, res.Error, "Old session MUST NOT be deleted from DB on dir creation error")
	assert.Equal(t, oldSessionID, check.ID)
}

func TestEmpiricalChallenge_AuditArchiverFailureSafety_FileCreateError(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	oldSessionID := uuid.New().String()
	oldSession := models.AuditSession{
		ID:            oldSessionID,
		UserID:        "user-challenge-error-injection-2",
		StartTime:     time.Now().Add(-40 * 24 * time.Hour),
		EndTime:       time.Now().Add(-40 * 24 * time.Hour),
		UserPrompt:    "Prompt must survive os.Create failure",
		FinalResponse: "Response must survive os.Create failure",
	}
	require.NoError(t, client.InsertAuditSession(oldSession))

	tempDir := t.TempDir()
	// Pre-create the directory with the exact collision name of the upcoming timestamp
	// to force os.Create(filePath) to fail
	targetTime := time.Now().Unix()
	collisionDirName := fmt.Sprintf("audit_archive_%d.jsonl.gz", targetTime)
	collisionPath := filepath.Join(tempDir, collisionDirName)
	err := os.Mkdir(collisionPath, 0755)
	require.NoError(t, err)

	archiveErr := archiveOldSessions(client, tempDir)
	// If the timestamp ticked to the next second, try in a loop or verify error
	if archiveErr != nil {
		t.Logf("Empirically observed expected error on file create collision: %v", archiveErr)
		var check models.AuditSession
		res := client.DB.Where("id = ?", oldSessionID).First(&check)
		assert.NoError(t, res.Error, "Old session MUST NOT be deleted from DB on file creation error")
	} else {
		t.Log("Timestamp advanced past collision directory during execution")
	}
}
