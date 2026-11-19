package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The nightly backup and /backup write the same kind of set an export writes: the dump, the
// archive files and a manifest. Taken from a running app, the manifest must not claim otherwise.
func TestLiveBackup_WritesASetThatSaysTheAppWasRunning(t *testing.T) {
	archiveDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "audit_archive_1700000000.jsonl.gz"), []byte("archive"), 0o644))
	t.Setenv("AUDIT_ARCHIVE_DIR", archiveDir)
	backupDir := t.TempDir()

	// Nothing listens on this address: the schema version cannot be read, the backup still happens.
	dumpPath, err := LiveBackup("postgres://mypa:pw@127.0.0.1:1/mypa?sslmode=disable&connect_timeout=2", backupDir,
		db.BackupOptions{Retention: db.DefaultRetention, Run: fakePostgresTools})
	require.NoError(t, err)

	assert.True(t, db.IsBackupFile(filepath.Base(dumpPath)))
	m, err := db.VerifyBackupSet(db.ManifestPathFor(dumpPath))
	require.NoError(t, err)
	assert.Equal(t, 1, m.ArchiveFiles)
	assert.Equal(t, int64(-1), m.SchemaVersion)
	require.NotNil(t, m.Source)
	assert.False(t, m.Source.Drained)
	assert.Equal(t, NotCarried, m.NotCarried)
}

func TestArchiveDirFromEnv(t *testing.T) {
	t.Setenv("AUDIT_ARCHIVE_DIR", "  /somewhere/archives ")
	assert.Equal(t, "/somewhere/archives", ArchiveDirFromEnv())

	t.Setenv("AUDIT_ARCHIVE_DIR", "")
	if _, err := os.Stat(DefaultArchiveDir); err != nil {
		assert.Empty(t, ArchiveDirFromEnv(), "no setting and no default directory: backups carry no archive files")
	}
}
