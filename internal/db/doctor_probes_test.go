package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SchemaVersion backs the /doctor schema check (V4-17).
func TestSchemaVersion(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRaw(t, newEmptyTestDatabase(t))

	// A database goose has never touched is at version 0, behind the build.
	current, latest, err := SchemaVersion(ctx, sqlDB)
	require.NoError(t, err)
	assert.Equal(t, int64(0), current)
	assert.Greater(t, latest, int64(0), "the build embeds at least the baseline migration")

	require.NoError(t, Migrate(ctx, sqlDB))

	current, latestAfter, err := SchemaVersion(ctx, sqlDB)
	require.NoError(t, err)
	assert.Equal(t, latest, latestAfter)
	assert.Equal(t, latest, current, "after migrating, the database is at the latest version")
}

// Reading the version must not create goose's table: a health check never changes the schema.
func TestSchemaVersion_DoesNotCreateTheVersionTable(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRaw(t, newEmptyTestDatabase(t))

	_, _, err := SchemaVersion(ctx, sqlDB)
	require.NoError(t, err)

	exists, err := tableExists(ctx, sqlDB, migrationTable)
	require.NoError(t, err)
	assert.False(t, exists)
}

// LatestBackupInfo backs the /doctor backup check: the newest backup's name and the time in it.
func TestLatestBackupInfo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"backup_2026-11-16_02-00-00.dump.age",
		"backup_2026-11-18_02-00-00.dump.age",
		"backup_2026-11-17_02-00-00.dump",
		"notes.txt",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}

	name, takenAt, err := LatestBackupInfo(dir)
	require.NoError(t, err)
	assert.Equal(t, "backup_2026-11-18_02-00-00.dump.age", name)
	assert.Equal(t, time.Date(2026, 11, 18, 2, 0, 0, 0, time.Local), takenAt)
}

func TestLatestBackupInfo_NoBackups(t *testing.T) {
	_, _, err := LatestBackupInfo(t.TempDir())
	require.Error(t, err)

	_, _, err = LatestBackupInfo(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
