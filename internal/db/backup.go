package db

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// BackupDatabase performs a pg_dump to the specified directory and maintains a rolling history of maxBackups.
func BackupDatabase(databaseURL string, backupDir string, maxBackups int) (string, error) {
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	fileName := fmt.Sprintf("backup_%s.dump", timestamp)
	backupPath := filepath.Join(backupDir, fileName)

	// Run pg_dump
	// -Fc specifies custom format (compressed, suitable for pg_restore)
	// -Z 5 specifies compression level
	cmd := exec.Command("pg_dump", "-d", databaseURL, "-F", "c", "-Z", "5", "-f", backupPath)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pg_dump failed: %s, error: %w", string(output), err)
	}

	slog.Info("Backup created successfully", "path", backupPath)

	// Manage rolling backups
	if err := enforceMaxBackups(backupDir, maxBackups); err != nil {
		slog.Error("Failed to enforce max backups", "error", err)
	}

	return backupPath, nil
}

// RestoreDatabase restores a backup file using pg_restore.
func RestoreDatabase(databaseURL string, backupPath string) error {
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupPath)
	}

	// -c cleans (drops) database objects before recreating them
	// --if-exists prevents errors if objects don't exist
	// -d specifies the target database
	cmd := exec.Command("pg_restore", "-c", "--if-exists", "-d", databaseURL, backupPath)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_restore failed: %s, error: %w", string(output), err)
	}

	slog.Info("Database restored successfully", "path", backupPath)
	return nil
}

// GetLatestBackup returns the path to the most recent backup file.
func GetLatestBackup(backupDir string) (string, error) {
	files, err := os.ReadDir(backupDir)
	if err != nil {
		return "", fmt.Errorf("failed to read backup directory: %w", err)
	}

	var backups []string
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".dump" {
			backups = append(backups, filepath.Join(backupDir, f.Name()))
		}
	}

	if len(backups) == 0 {
		return "", fmt.Errorf("no backup files found in %s", backupDir)
	}

	sort.Strings(backups)
	return backups[len(backups)-1], nil
}

func enforceMaxBackups(backupDir string, maxBackups int) error {
	files, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}

	var backups []os.DirEntry
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".dump" {
			backups = append(backups, f)
		}
	}

	if len(backups) <= maxBackups {
		return nil
	}

	// Sort backups by name (which includes timestamp, so chronological order)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Name() < backups[j].Name()
	})

	// Delete the oldest files until we are at maxBackups
	filesToDelete := len(backups) - maxBackups
	for i := 0; i < filesToDelete; i++ {
		path := filepath.Join(backupDir, backups[i].Name())
		if err := os.Remove(path); err != nil {
			slog.Error("Failed to delete old backup", "path", path, "error", err)
		} else {
			slog.Info("Deleted old backup", "path", path)
		}
	}

	return nil
}
