package transfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
)

// DefaultArchiveDir is where the audit worker writes archive files inside its container, and
// where the orchestrator sees them (mounted read-only) for backups.
const DefaultArchiveDir = "/app/archives"

// ArchiveDirFromEnv returns the directory holding archive files: AUDIT_ARCHIVE_DIR when set,
// otherwise DefaultArchiveDir when it exists, otherwise "" (backups then carry no archive files).
func ArchiveDirFromEnv() string {
	if dir := strings.TrimSpace(os.Getenv("AUDIT_ARCHIVE_DIR")); dir != "" {
		return dir
	}
	if info, err := os.Stat(DefaultArchiveDir); err == nil && info.IsDir() {
		return DefaultArchiveDir
	}
	return ""
}

// LiveBackup takes a backup set of a running install: the nightly backup and /backup. It is the
// same set an export writes (so it restores through the same import), except that the app was
// not stopped: the manifest records that, and messages in flight at that moment are not in it.
// It returns the path of the database dump, which names the set.
func LiveBackup(databaseURL, backupDir string, opts db.BackupOptions) (string, error) {
	m, manifestPath, err := db.CreateBackupSet(databaseURL, backupDir, opts, db.SetOptions{
		ArchiveDir:    ArchiveDirFromEnv(),
		SchemaVersion: liveSchemaVersion(databaseURL),
		Source:        &db.SourceInfo{Drained: false},
		NotCarried:    NotCarried,
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(manifestPath), m.Database.Name), nil
}

// liveSchemaVersion reads the schema version for the manifest; -1 when it cannot be read (the
// backup is still taken: a missing number must not cost a backup).
func liveSchemaVersion(databaseURL string) int64 {
	client, err := db.Open(databaseURL)
	if err != nil {
		return -1
	}
	defer client.Close()
	sqlDB, err := client.DB.DB()
	if err != nil {
		return -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, _, err := db.SchemaVersion(ctx, sqlDB)
	if err != nil {
		return -1
	}
	return current
}
