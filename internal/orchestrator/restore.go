package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/transfer"
)

// BL-21: the chat restore is retired. A restore needs the app stopped, an emptied schema and the
// migrations afterwards, which only the restore tool (`transfer import`) does. /restore stays as
// an admin command that names the newest backup and replies with the steps for the tool.

const (
	// defaultBackupDir is the backup volume mounted in docker-compose.yml.
	defaultBackupDir = "/backups"
	// legacyRestoreActionName is the pending-action name the retired chat restore staged in Redis.
	// A record with this name left from before the upgrade is dropped on confirmation, never run.
	legacyRestoreActionName = "restore"
	// restoreGuide is the procedure the /restore reply points to.
	restoreGuide = "docs/RESTORE_AND_MIGRATE.md"
)

// backupOps abstracts pg_dump and the backup directory so the commands can be tested without a DB.
type backupOps struct {
	dir    string
	backup func(databaseURL, dir string) (string, error)
	latest func(dir string) (string, error)
}

// defaultBackupOps uses the real pg_dump with encryption keys and retention from the environment
// (BACKUP_AGE_RECIPIENTS; P3-4).
func defaultBackupOps() backupOps {
	return backupOps{
		dir: defaultBackupDir,
		backup: func(databaseURL, dir string) (string, error) {
			opts, err := db.BackupOptionsFromEnv()
			if err != nil {
				return "", err
			}
			// A backup set (V4-43): the dump plus the archive files and a manifest.
			return transfer.LiveBackup(databaseURL, dir, opts)
		},
		latest: db.GetLatestBackup,
	}
}

func (b backupOps) withDefaults() backupOps {
	d := defaultBackupOps()
	if b.dir == "" {
		b.dir = d.dir
	}
	if b.backup == nil {
		b.backup = d.backup
	}
	if b.latest == nil {
		b.latest = d.latest
	}
	return b
}

// handleRestoreCommand replies with the steps to restore the newest backup with the restore tool.
// It changes nothing: no backup is taken, nothing is staged and nothing is restored.
func (e *Engine) handleRestoreCommand(ctx context.Context, user *models.User, msg models.Message) error {
	if user.Role != "admin" {
		slog.Warn("unauthorized restore command attempt", "user_id", user.PlatformID, "role", user.Role)
		return e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
	}

	ops := e.backups.withDefaults()
	latest, err := ops.latest(ops.dir)
	switch {
	case errors.Is(err, db.ErrNoBackup):
		return e.sendMessage(ctx, msg, "📭 No backup found in the backup directory, so there is nothing to restore. "+
			"`/backup` takes one now. Restoring is described in `"+restoreGuide+"`.")
	case err != nil:
		// The error text holds the directory path: log it, do not show it.
		slog.Error("restore instructions: backup directory could not be read", "error", err)
		return e.sendMessage(ctx, msg, "❌ The backup directory could not be read. `/doctor` checks the backups; "+
			"the app log has the details.")
	}
	return e.sendMessage(ctx, msg, restoreInstructions(latest))
}

// restoreInstructions is the /restore reply for the newest backup at latestPath. It shows only file
// names, never the directory, and the fixed commands of the documented procedure.
func restoreInstructions(latestPath string) string {
	dumpName := filepath.Base(latestPath)
	target := dumpName
	if _, err := os.Stat(db.ManifestPathFor(latestPath)); err == nil {
		target = filepath.Base(db.ManifestPathFor(latestPath)) // a backup set (V4-43)
	}
	keyEnv, keyStep, lastStep := "", "", "Start the app with `docker compose up -d`, then send `/doctor`."
	if strings.HasSuffix(dumpName, ".age") {
		keyEnv = "-e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt "
		keyStep = "Put the private backup key in `backups/backup-key.txt` for the duration of the restore.\n"
		lastStep = "Delete `backups/backup-key.txt`, start the app with `docker compose up -d`, then send `/doctor`."
	}

	var sb strings.Builder
	sb.WriteString("🛟 **Restoring is done from the terminal**\n\n")
	sb.WriteString("A restore needs the app stopped, so it cannot run from chat. Newest backup: `" + target + "`\n\n")
	sb.WriteString("On the machine that runs MyPA:\n\n")
	sb.WriteString("1. Stop the app: `docker compose stop ngrok` so nothing new comes in, wait a few seconds, then " +
		"`docker compose stop app audit-worker dashboard`.\n")
	step := 2
	if keyStep != "" {
		sb.WriteString(fmt.Sprintf("%d. %s", step, keyStep))
		step++
	}
	sb.WriteString(fmt.Sprintf("%d. Restore it (a safety backup of the current database is taken first):\n"+
		"`docker compose --profile tools run --rm %stransfer import %s --replace`\n", step, keyEnv, target))
	step++
	sb.WriteString(fmt.Sprintf("%d. %s\n\n", step, lastStep))
	sb.WriteString("Full steps: `" + restoreGuide + "`, section C.")
	return sb.String()
}
