// Package transfer is the restore-and-migrate mechanism (ROADMAP V4-43, DE-10): one path for
// upgrading from the pre-V4 baseline, moving an install between machines, and disaster recovery.
//
// Export takes a final backup set of a stopped install (the database dump, the archive files and
// a manifest; see db.CreateBackupSet). Import restores a set, or a bare dump from before sets
// existed, into an install and lets the versioned migrations bring it up to date. The backup that
// was restored is never modified, so it remains the rollback.
//
// The package holds the procedure and its safety rules. What it runs against (Postgres, RabbitMQ,
// Redis, the file system) is passed in, so every rule is tested without them, and the whole
// procedure is tested end to end against throwaway containers in cmd/orchestrator.
package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nivik/mypa/internal/db"
)

// NotCarried is the state a backup does not hold and a restore therefore does not bring back.
// Until Postgres is the only state store (V4-23) it lives in Redis and RabbitMQ. The list is
// written into every manifest and printed after every export and import.
var NotCarried = []string{
	"The conversation context of the last 24 hours (the chat window): the assistant remembers saved facts, but starts each conversation without the recent back-and-forth.",
	"Actions that were waiting for a CONFIRM reply, including a staged /restore: ask again.",
	"Google sign-in links that were sent but not finished: send /connect again.",
	"Pairing lockouts and failed-attempt counters: they start from zero.",
	"Follow-up tool context and cached prompt sections: rebuilt on the next message.",
	"The memory of already-handled message IDs: a message a platform re-delivers across the move could be answered twice.",
	"WhatsApp's own 24-hour reply window is kept by WhatsApp, not by MyPA: someone who has not written for a day cannot be messaged until they write.",
}

// QueueProbe reports how many messages wait in a queue and how many consumers are attached.
// A queue that does not exist reports zero for both.
type QueueProbe func(ctx context.Context, queue string) (ready, consumers int, err error)

// stateOf looks at every queue. stopped is true when none has a consumer and none holds a message.
func stateOf(ctx context.Context, probe QueueProbe, queues []string) (info map[string]db.QueueInfo, stopped bool, err error) {
	info = map[string]db.QueueInfo{}
	stopped = true
	for _, q := range queues {
		ready, consumers, err := probe(ctx, q)
		if err != nil {
			return nil, false, fmt.Errorf("queue %s: %w", q, err)
		}
		info[q] = db.QueueInfo{Ready: ready, Consumers: consumers}
		if ready > 0 || consumers > 0 {
			stopped = false
		}
	}
	return info, stopped, nil
}

// describeBusy says, queue by queue, why an install does not count as stopped.
func describeBusy(info map[string]db.QueueInfo) string {
	names := make([]string, 0, len(info))
	for q := range info {
		names = append(names, q)
	}
	sort.Strings(names)
	var parts []string
	for _, q := range names {
		s := info[q]
		if s.Consumers > 0 {
			parts = append(parts, fmt.Sprintf("%s is being consumed (%d consumer(s): the app is still running)", q, s.Consumers))
		}
		if s.Ready > 0 {
			parts = append(parts, fmt.Sprintf("%s still holds %d message(s)", q, s.Ready))
		}
	}
	return strings.Join(parts, "; ")
}

// Exporter takes the final backup set of an install that is being upgraded or moved.
type Exporter struct {
	DatabaseURL string
	BackupDir   string
	// ArchiveDir holds the archive files (audit archives). Empty: the set carries none.
	ArchiveDir string
	Backup     db.BackupOptions

	// AllowUnencrypted permits a set without age encryption. Off by default: a set holds every
	// conversation and every stored secret, and it is made to leave the machine.
	AllowUnencrypted bool

	// Queues and Probe check that the install is stopped and drained. SkipStopCheck turns the
	// check off for the case where the broker itself is gone (disaster recovery).
	Queues        []string
	Probe         QueueProbe
	SkipStopCheck bool

	// SchemaVersion reads the migration version of the database being exported.
	SchemaVersion func(ctx context.Context) (int64, error)
	// SyncSignIns makes sure every Google sign-in has its encrypted copy in the database before
	// the dump (see SignInSync). Nil: the step is skipped and the manifest says nothing about it.
	SyncSignIns func(ctx context.Context) (db.SignInInfo, error)

	Out io.Writer
}

// Run performs the export and returns the manifest and its path.
func (e Exporter) Run(ctx context.Context) (db.Manifest, string, error) {
	out := e.Out
	if out == nil {
		out = io.Discard
	}
	if len(e.Backup.Recipients) == 0 && !e.AllowUnencrypted {
		return db.Manifest{}, "", errors.New("refusing to export without encryption: set BACKUP_AGE_RECIPIENTS to an age public key " +
			"(the set holds every conversation and stored secret), or pass --allow-unencrypted if the file never leaves a disk you trust")
	}

	source := &db.SourceInfo{}
	if e.SkipStopCheck {
		fmt.Fprintln(out, "! Stop check skipped: this backup may miss messages that were still in flight.")
	} else {
		if e.Probe == nil {
			return db.Manifest{}, "", errors.New("no way to check the queues was provided")
		}
		info, stopped, err := stateOf(ctx, e.Probe, e.Queues)
		if err != nil {
			return db.Manifest{}, "", fmt.Errorf("cannot confirm the install is stopped: %w (if RabbitMQ itself is gone, pass --skip-stop-check)", err)
		}
		if !stopped {
			return db.Manifest{}, "", fmt.Errorf("the install is not stopped and drained: %s. "+
				"Stop the gateway first, wait until the queues are empty, then stop the orchestrator and the audit worker, and run the export again",
				describeBusy(info))
		}
		source.Stopped, source.Queues = true, info
		fmt.Fprintln(out, "✓ The app is stopped and the queues are empty.")
	}

	if e.SyncSignIns != nil {
		signIns, err := e.SyncSignIns(ctx)
		if err != nil {
			return db.Manifest{}, "", fmt.Errorf("cannot make sure Google sign-ins are in the backup: %w", err)
		}
		source.GoogleSignIns = &signIns
		fmt.Fprintf(out, "✓ Google sign-ins in the database: %d (%d copied from the cache just now).\n", signIns.InDatabase, signIns.CopiedNow)
		if signIns.Unreadable > 0 {
			fmt.Fprintf(out, "! %d cached Google sign-in(s) could not be read and are not in the backup: those people will need /connect after the restore.\n", signIns.Unreadable)
		}
	}

	version := int64(-1)
	if e.SchemaVersion != nil {
		v, err := e.SchemaVersion(ctx)
		if err != nil {
			return db.Manifest{}, "", fmt.Errorf("cannot read the schema version: %w", err)
		}
		version = v
	}

	m, manifestPath, err := db.CreateBackupSet(e.DatabaseURL, e.BackupDir, e.Backup, db.SetOptions{
		ArchiveDir:    e.ArchiveDir,
		SchemaVersion: version,
		Source:        source,
		NotCarried:    NotCarried,
	})
	if err != nil {
		return db.Manifest{}, "", err
	}
	// Read the set back from disk: what was written is what will be restored.
	if _, err := db.VerifyBackupSet(manifestPath); err != nil {
		return db.Manifest{}, "", fmt.Errorf("the backup set did not verify after it was written: %w", err)
	}

	fmt.Fprintf(out, "✓ Backup set written to %s:\n", filepath.Dir(manifestPath))
	fmt.Fprintf(out, "    %s  (database, schema version %s)\n", m.Database.Name, versionText(m.SchemaVersion))
	if m.Archives != nil {
		fmt.Fprintf(out, "    %s  (%d archive file(s))\n", m.Archives.Name, m.ArchiveFiles)
	} else {
		fmt.Fprintln(out, "    (no archive files were found to include)")
	}
	fmt.Fprintf(out, "    %s\n", filepath.Base(manifestPath))
	if !m.Encrypted {
		fmt.Fprintln(out, "! This set is NOT encrypted. Anyone who can read these files can read every conversation.")
	}
	printNotCarried(out, m.NotCarried)
	fmt.Fprintln(out, "Next: copy the three files to the new install's backup directory, bring the keys (transfer export-keys), and run: transfer import "+filepath.Base(manifestPath))
	return m, manifestPath, nil
}

// versionText renders a schema version; a set taken while the version could not be read has -1.
func versionText(v int64) string {
	if v < 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", v)
}

func printNotCarried(out io.Writer, items []string) {
	if len(items) == 0 {
		items = NotCarried
	}
	fmt.Fprintln(out, "Not carried by a backup (short-lived state that lives outside the database):")
	for _, item := range items {
		fmt.Fprintf(out, "  - %s\n", item)
	}
}

// Importer restores a backup into an install and brings the schema up to date.
type Importer struct {
	DatabaseURL string
	// ArchiveDir receives the archive files. Empty: they are not restored (and the report says so).
	ArchiveDir string
	// BackupDir receives the safety backup taken before a non-empty database is replaced.
	BackupDir string
	Restore   db.RestoreOptions
	// Backup configures that safety backup.
	Backup db.BackupOptions

	// SkipMigrate leaves the restored database at the schema version it was backed up with.
	// That is the rollback: restore the backup taken before an upgrade, then start the release
	// that took it. This build must not be started on such a database.
	SkipMigrate bool

	// Replace allows restoring over a database that already holds users. Without it such a
	// target is refused. With it, a safety backup is taken first and the restore is abandoned
	// if that backup fails.
	Replace bool

	Queues        []string
	Probe         QueueProbe
	SkipStopCheck bool

	// DB is the target database.
	DB *sql.DB
	// Migrate brings the restored schema up to date (db.Migrate).
	Migrate func(ctx context.Context, sqlDB *sql.DB) error
	// SchemaVersion reads the target's current version and the latest this build knows.
	SchemaVersion func(ctx context.Context, sqlDB *sql.DB) (current, latest int64, err error)
	// CheckDataKey tries to decrypt stored secrets with this install's data encryption key and
	// returns how many it tried and how many failed. Nil: not checked.
	CheckDataKey func(ctx context.Context, sqlDB *sql.DB) (checked, failed int, err error)
	// CountSignIns returns how many Google sign-ins the restored database holds. Nil: not reported.
	CountSignIns func(ctx context.Context, sqlDB *sql.DB) (int, error)

	Out io.Writer
}

// Result is what an import did.
type Result struct {
	// Source is the file the database was restored from.
	Source string
	// FromSet is false for a bare dump (no manifest, no archive files).
	FromSet bool
	// SchemaBefore is the version right after the restore; SchemaAfter after the migrations.
	SchemaBefore, SchemaAfter int64
	// SafetyBackup is the backup of the replaced database; empty when the target was empty.
	SafetyBackup                      string
	ArchivesRestored, ArchivesInPlace int
	GoogleSignIns                     int
	// Problems are things that went wrong without stopping the restore. A restore with problems
	// is not finished: each one says what to do.
	Problems []string
}

// Run restores the backup at path: a set's manifest, or a database dump.
func (i Importer) Run(ctx context.Context, path string) (Result, error) {
	out := i.Out
	if out == nil {
		out = io.Discard
	}
	res := Result{}

	// 1. What are we restoring? Verify it before touching anything.
	dumpPath, archivePath, manifest, err := resolveBackup(path)
	if err != nil {
		return res, err
	}
	res.Source, res.FromSet = dumpPath, manifest != nil
	if res.FromSet {
		fmt.Fprintf(out, "✓ Backup set verified: %s (taken %s, schema version %s).\n",
			filepath.Base(dumpPath), manifest.CreatedAt.Format("2006-01-02 15:04 MST"), versionText(manifest.SchemaVersion))
		if manifest.Source != nil && !manifest.Source.Stopped {
			fmt.Fprintln(out, "! This set was taken without confirming that the source was stopped: messages in flight at that moment are not in it.")
		}
	} else {
		fmt.Fprintf(out, "✓ Restoring a database dump without a manifest: %s. It carries no archive files.\n", filepath.Base(dumpPath))
	}
	encrypted := strings.HasSuffix(dumpPath, ".age") || strings.HasSuffix(archivePath, ".age")
	if encrypted && len(i.Restore.Identities) == 0 {
		return res, errors.New("the backup is encrypted: set BACKUP_AGE_IDENTITY_FILE to the age private key (the backup key) and run the import again")
	}

	// 2. The target must not be running: two live installs on one Telegram bot or WhatsApp
	// number would both answer, and a running app would write into the database being replaced.
	if i.SkipStopCheck {
		fmt.Fprintln(out, "! Stop check skipped.")
	} else {
		if i.Probe == nil {
			return res, errors.New("no way to check the queues was provided")
		}
		info, _, err := stateOf(ctx, i.Probe, i.Queues)
		if err != nil {
			return res, fmt.Errorf("cannot confirm this install is stopped: %w (if RabbitMQ is not running yet, start it, or pass --skip-stop-check)", err)
		}
		for q, s := range info {
			if s.Consumers > 0 {
				return res, fmt.Errorf("this install is running (%s has %d consumer(s)). Stop the gateway, the orchestrator and the audit worker, then run the import again", q, s.Consumers)
			}
		}
		fmt.Fprintln(out, "✓ This install is stopped.")
	}

	// 3. Never replace data silently.
	hasData, err := db.HasUserData(ctx, i.DB)
	if err != nil {
		return res, fmt.Errorf("cannot inspect the target database: %w", err)
	}
	if hasData {
		if !i.Replace {
			return res, errors.New("the target database already holds users. Restoring would replace everything in it. " +
				"If that is what you want, run the import again with --replace: a safety backup of the current database is taken first")
		}
		// No pruning here: retention could delete the backup that is about to be restored.
		safetyOptions := i.Backup
		safetyOptions.SkipRetention = true
		safety, err := db.BackupDatabase(i.DatabaseURL, i.BackupDir, safetyOptions)
		if err != nil {
			return res, fmt.Errorf("the safety backup of the current database failed, so nothing was changed: %w", err)
		}
		res.SafetyBackup = safety
		fmt.Fprintf(out, "✓ Safety backup of the current database: %s\n", filepath.Base(safety))
	}

	// 4. Restore into an empty schema, then let the migrations bring it up to date.
	if err := db.ResetPublicSchema(ctx, i.DB); err != nil {
		return res, fmt.Errorf("cannot empty the target database: %w", err)
	}
	restore := i.Restore
	restore.FreshSchema = true
	if err := db.RestoreDatabase(i.DatabaseURL, dumpPath, restore); err != nil {
		hint := "The target database is empty now; fix the cause and run the import again."
		if res.SafetyBackup != "" {
			hint = fmt.Sprintf("The target database is empty now. Its previous contents are in %s; import that file to get them back.", filepath.Base(res.SafetyBackup))
		}
		return res, fmt.Errorf("the restore failed: %w. %s", err, hint)
	}
	if res.SchemaBefore, _, err = i.SchemaVersion(ctx, i.DB); err != nil {
		return res, fmt.Errorf("the data was restored, but its schema version cannot be read: %w", err)
	}
	if i.SkipMigrate {
		res.SchemaAfter = res.SchemaBefore
		fmt.Fprintf(out, "✓ Database restored and left at schema version %d, as asked. Start the release that took this backup, not this one.\n", res.SchemaBefore)
	} else {
		if err := i.Migrate(ctx, i.DB); err != nil {
			return res, fmt.Errorf("the data was restored at schema version %d, but bringing it up to date failed: %w. "+
				"The backup itself is untouched; fix the migration and run the import again", res.SchemaBefore, err)
		}
		var latest int64
		if res.SchemaAfter, latest, err = i.SchemaVersion(ctx, i.DB); err != nil {
			return res, fmt.Errorf("the data was restored and migrated, but its schema version cannot be read: %w", err)
		}
		if res.SchemaAfter != latest {
			return res, fmt.Errorf("after the migrations the database is at version %d, but this build expects %d", res.SchemaAfter, latest)
		}
		if res.SchemaBefore == res.SchemaAfter {
			fmt.Fprintf(out, "✓ Database restored. Schema version %d is current.\n", res.SchemaAfter)
		} else {
			fmt.Fprintf(out, "✓ Database restored and migrated from schema version %d to %d.\n", res.SchemaBefore, res.SchemaAfter)
		}
	}

	// 5. Archive files.
	switch {
	case archivePath == "":
		// A bare dump, or a set taken on an install without archives: nothing to do.
	case i.ArchiveDir == "":
		res.Problems = append(res.Problems, "The set contains archive files, but no archive directory is configured, so they were not restored. Set AUDIT_ARCHIVE_DIR and run the import again.")
	default:
		res.ArchivesRestored, res.ArchivesInPlace, err = db.ExtractArchives(archivePath, i.ArchiveDir, i.Restore.Identities)
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("The database is restored, but the archive files are not complete: %v. Fix the cause and run the import again (files already restored are left alone).", err))
		} else {
			fmt.Fprintf(out, "✓ Archive files: %d restored, %d already in place.\n", res.ArchivesRestored, res.ArchivesInPlace)
		}
	}

	// 6. Do the secrets in the restored data open with this install's key?
	if i.CountSignIns != nil {
		if n, err := i.CountSignIns(ctx, i.DB); err == nil {
			res.GoogleSignIns = n
		}
	}
	if i.CheckDataKey != nil {
		checked, failed, err := i.CheckDataKey(ctx, i.DB)
		switch {
		case err != nil:
			res.Problems = append(res.Problems, fmt.Sprintf("Could not check whether this install's ENCRYPTION_KEY opens the restored secrets: %v.", err))
		case failed > 0:
			res.Problems = append(res.Problems, fmt.Sprintf("This install's ENCRYPTION_KEY does not open %d of %d stored secret(s). "+
				"Google sign-ins and API connector secrets will not work until ENCRYPTION_KEY is the one the data was encrypted with: "+
				"bring it over with `transfer export-keys` on the old install and `transfer import-keys` here. The restored data itself is fine; no need to import again.", failed, checked))
		case checked > 0:
			fmt.Fprintf(out, "✓ This install's ENCRYPTION_KEY opens the restored secrets (%d checked). Google sign-ins carried over: %d.\n", checked, res.GoogleSignIns)
		default:
			fmt.Fprintln(out, "✓ The restored data holds no stored secrets to check.")
		}
	}

	var notCarried []string
	if manifest != nil {
		notCarried = manifest.NotCarried
	}
	printNotCarried(out, notCarried)
	for _, p := range res.Problems {
		fmt.Fprintf(out, "! %s\n", p)
	}
	if len(res.Problems) == 0 {
		if i.SkipMigrate {
			fmt.Fprintln(out, "Next: check out and start the release that took this backup. Do not start this release on this database.")
		} else {
			fmt.Fprintln(out, "Next: start the app (docker compose up -d) and send /doctor as an admin. The old install must stay stopped.")
		}
	} else {
		fmt.Fprintln(out, "The restore is NOT finished: resolve the points marked with ! above before starting the app.")
	}
	return res, nil
}

// resolveBackup turns the path given to an import into the files to restore. A manifest, or a
// dump that has one next to it, is verified as a set. A dump without a manifest (a backup from
// before sets existed, such as the pre-V4 baseline) is restored on its own.
func resolveBackup(path string) (dumpPath, archivePath string, manifest *db.Manifest, err error) {
	name := filepath.Base(path)
	manifestPath := ""
	switch {
	case db.IsManifestFile(name):
		manifestPath = path
	case db.IsBackupFile(name):
		if candidate := db.ManifestPathFor(path); fileExists(candidate) {
			manifestPath = candidate
		} else if !fileExists(path) {
			return "", "", nil, fmt.Errorf("backup file not found: %s", name)
		} else {
			return path, "", nil, nil
		}
	default:
		return "", "", nil, fmt.Errorf("%s is neither a backup manifest (backup_<time>.manifest.json) nor a database dump (backup_<time>.dump[.age])", name)
	}
	m, err := db.VerifyBackupSet(manifestPath)
	if err != nil {
		return "", "", nil, err
	}
	dir := filepath.Dir(manifestPath)
	dumpPath = filepath.Join(dir, m.Database.Name)
	if m.Archives != nil {
		archivePath = filepath.Join(dir, m.Archives.Name)
	}
	return dumpPath, archivePath, &m, nil
}
