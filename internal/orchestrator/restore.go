package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
)

const (
	// restoreActionName is the pending-action name staged by /restore and redeemed by
	// "CONFIRM RESTORE". It is not an LLM tool name, so the model can never stage or run it.
	restoreActionName = "restore"
	// restoreConfirmTTL bounds how long a staged restore can be confirmed.
	restoreConfirmTTL = 5 * time.Minute
	// defaultBackupDir is the backup volume mounted in docker-compose.yml.
	defaultBackupDir = "/backups"
	// backupRetention mirrors the scheduler's rolling retention.
	backupRetention = 3
)

// backupOps abstracts pg_dump/pg_restore so the destructive flow can be tested without a DB.
type backupOps struct {
	dir     string
	backup  func(databaseURL, dir string, keep int) (string, error)
	restore func(databaseURL, path string) error
	latest  func(dir string) (string, error)
}

func defaultBackupOps() backupOps {
	return backupOps{
		dir:     defaultBackupDir,
		backup:  db.BackupDatabase,
		restore: db.RestoreDatabase,
		latest:  db.GetLatestBackup,
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
	if b.restore == nil {
		b.restore = d.restore
	}
	if b.latest == nil {
		b.latest = d.latest
	}
	return b
}

// handleRestoreCommand stages a restore of the latest backup and asks for explicit
// confirmation. It never touches the database itself.
func (e *Engine) handleRestoreCommand(ctx context.Context, user *models.User, msg models.Message) error {
	if user.Role != "admin" {
		slog.Warn("unauthorized restore command attempt", "user_id", user.PlatformID, "role", user.Role)
		return e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
	}
	if os.Getenv("DATABASE_URL") == "" {
		return e.sendMessage(ctx, msg, "❌ Restore failed: DATABASE_URL not set.")
	}
	if e.store == nil {
		return e.sendMessage(ctx, msg, "❌ Restore unavailable: confirmation store not configured.")
	}

	ops := e.backups.withDefaults()
	latest, err := ops.latest(ops.dir)
	if err != nil {
		slog.Error("failed to find latest backup", "error", err)
		return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Restore failed: %v", err))
	}
	created := "unknown time"
	if fi, statErr := os.Stat(latest); statErr == nil {
		created = fi.ModTime().UTC().Format("2006-01-02 15:04 MST")
	}

	// Stage the exact file shown to the admin. Confirmation must restore THIS file: by then a
	// safety backup will exist and would otherwise be picked as "latest".
	if err := e.store.StagePendingMetaTool(ctx, msg.UserID, state.PendingMetaToolAction{
		ToolName:  restoreActionName,
		Args:      map[string]interface{}{"backup_path": latest},
		UserID:    msg.UserID,
		CreatedAt: time.Now(),
	}, restoreConfirmTTL); err != nil {
		slog.Error("failed to stage restore confirmation", "error", err)
		return e.sendMessage(ctx, msg, "❌ Could not stage the restore. Please try again.")
	}

	return e.sendMessage(ctx, msg, fmt.Sprintf(
		"⚠️ *Database restore requested*\n\n"+
			"This will REPLACE the entire database with:\n📁 `%s`\n🕒 created %s\n\n"+
			"Everything changed since then will be lost. A safety backup of the current database is taken first.\n\n"+
			"👉 Reply `CONFIRM RESTORE` within %d minutes to proceed.",
		filepath.Base(latest), created, int(restoreConfirmTTL.Minutes())))
}

// executeConfirmedRestore runs a staged restore after "CONFIRM RESTORE": it re-checks
// authorization, takes a safety backup, and only then restores the staged file.
func (e *Engine) executeConfirmedRestore(ctx context.Context, user *models.User, msg models.Message, pending *state.PendingMetaToolAction) error {
	if user == nil || user.Role != "admin" {
		slog.Warn("restore confirmation by non-admin rejected", "user_id", msg.UserID)
		return e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return e.sendMessage(ctx, msg, "❌ Restore failed: DATABASE_URL not set.")
	}

	ops := e.backups.withDefaults()
	target, _ := pending.Args["backup_path"].(string)
	// Defense in depth: the staged record lives in Redis; only accept a .dump file directly
	// inside the backup directory.
	if target == "" || filepath.Ext(target) != ".dump" || filepath.Clean(filepath.Dir(target)) != filepath.Clean(ops.dir) {
		slog.Error("rejecting staged restore with invalid backup path", "path", target)
		return e.sendMessage(ctx, msg, "❌ Restore aborted: the staged backup path is invalid. Run /restore again.")
	}
	if _, err := os.Stat(target); err != nil {
		return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Restore aborted: backup `%s` no longer exists. Run /restore again.", filepath.Base(target)))
	}

	markSideEffect(ctx)
	_ = e.sendMessage(ctx, msg, "🔄 Taking a safety backup of the current database before restoring...")
	safety, err := ops.backup(databaseURL, ops.dir, backupRetention+1) // +1 so the safety copy never evicts the target
	if err != nil {
		slog.Error("pre-restore safety backup failed; restore aborted", "error", err)
		return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Restore aborted: safety backup failed (%v). The database was not changed.", err))
	}

	slog.Warn("executing confirmed database restore", "user_id", msg.UserID, "backup", target, "safety_backup", safety)
	if err := ops.restore(databaseURL, target); err != nil {
		slog.Error("confirmed restore failed", "error", err, "safety_backup", safety)
		return e.sendMessage(ctx, msg, fmt.Sprintf("❌ Restore failed: %v\n🛟 Safety backup of the previous state: `%s`", err, filepath.Base(safety)))
	}
	return e.sendMessage(ctx, msg, fmt.Sprintf("✅ Restore completed from `%s`.\n🛟 Safety backup of the previous state: `%s`",
		filepath.Base(target), filepath.Base(safety)))
}

// isDestructiveAction lists staged actions that require the action name in the confirmation
// ("CONFIRM RESTORE"); a bare "CONFIRM" is not enough.
func isDestructiveAction(name string) bool {
	return strings.EqualFold(name, restoreActionName)
}
