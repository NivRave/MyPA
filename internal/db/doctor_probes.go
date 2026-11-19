package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// Read-only probes for the /doctor health check (V4-17).

// SchemaVersion returns the migration version the database is at and the latest version this
// build embeds. It only reads: on a database goose has never touched it reports version 0
// without creating goose's bookkeeping table.
func SchemaVersion(ctx context.Context, sqlDB *sql.DB) (current, latest int64, err error) {
	provider, err := newMigrationProvider(sqlDB)
	if err != nil {
		return 0, 0, err
	}
	for _, source := range provider.ListSources() {
		if source.Version > latest {
			latest = source.Version
		}
	}

	hasVersions, err := tableExists(ctx, sqlDB, migrationTable)
	if err != nil {
		return 0, latest, fmt.Errorf("look for the migration table: %w", err)
	}
	if !hasVersions {
		return 0, latest, nil
	}
	store, err := database.NewStore(goose.DialectPostgres, migrationTable)
	if err != nil {
		return 0, latest, err
	}
	current, err = store.GetLatestVersion(ctx, sqlDB)
	if err != nil {
		return 0, latest, fmt.Errorf("read the schema version: %w", err)
	}
	return current, latest, nil
}

// LatestBackupInfo returns the file name of the newest backup in backupDir and the time encoded
// in that name (when the backup was taken, in the local time zone of the process that took it).
func LatestBackupInfo(backupDir string) (name string, takenAt time.Time, err error) {
	path, err := GetLatestBackup(backupDir)
	if err != nil {
		return "", time.Time{}, err
	}
	name = filepath.Base(path)
	takenAt, ok := parseBackupTime(name)
	if !ok {
		return "", time.Time{}, fmt.Errorf("backup %s has no readable timestamp", name)
	}
	return name, takenAt, nil
}
