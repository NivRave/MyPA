package db

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

const (
	backupTimeLayout = "2006-01-02_15-04-05"
	backupPrefix     = "backup_"
	plainExt         = ".dump"
	encryptedExt     = ".dump.age"
)

// RetentionPolicy is grandfather-father-son retention (P3-4): always keep the KeepLatest newest
// backups, plus the newest backup of each of the last Daily days, plus the newest of each of the
// last Weekly ISO weeks. KeepLatest matters: a /restore takes a safety backup on the same day as
// the file it is about to restore, and a per-day rule alone would prune that file.
type RetentionPolicy struct {
	KeepLatest int
	Daily      int
	Weekly     int
}

// DefaultRetention: 3 latest + 7 daily + 4 weekly.
var DefaultRetention = RetentionPolicy{KeepLatest: 3, Daily: 7, Weekly: 4}

// BackupOptions controls a backup run.
type BackupOptions struct {
	Retention RetentionPolicy
	// Recipients encrypt the dump with age. Empty → unencrypted (a warning is logged).
	Recipients []age.Recipient
}

// RestoreOptions controls a restore run.
type RestoreOptions struct {
	// Identities decrypt .dump.age archives.
	Identities []age.Identity
}

// BackupOptionsFromEnv reads BACKUP_AGE_RECIPIENTS (comma-separated age public keys, "age1…").
func BackupOptionsFromEnv() (BackupOptions, error) {
	opts := BackupOptions{Retention: DefaultRetention}
	raw := strings.TrimSpace(os.Getenv("BACKUP_AGE_RECIPIENTS"))
	if raw == "" {
		return opts, nil
	}
	for _, r := range strings.Split(raw, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		rec, err := age.ParseX25519Recipient(r)
		if err != nil {
			return opts, fmt.Errorf("invalid BACKUP_AGE_RECIPIENTS entry: %w", err)
		}
		opts.Recipients = append(opts.Recipients, rec)
	}
	return opts, nil
}

// RestoreOptionsFromEnv reads the age private key file named by BACKUP_AGE_IDENTITY_FILE.
// Keep this key OFF the server where possible (mount it only when restoring).
func RestoreOptionsFromEnv() (RestoreOptions, error) {
	path := strings.TrimSpace(os.Getenv("BACKUP_AGE_IDENTITY_FILE"))
	if path == "" {
		return RestoreOptions{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return RestoreOptions{}, fmt.Errorf("open BACKUP_AGE_IDENTITY_FILE: %w", err)
	}
	defer f.Close()
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return RestoreOptions{}, fmt.Errorf("parse BACKUP_AGE_IDENTITY_FILE: %w", err)
	}
	return RestoreOptions{Identities: ids}, nil
}

// runCommand executes an external program. A package variable so tests can replace pg_dump /
// pg_restore without a database.
var runCommand = func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %s: %w", name, strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// pgConnArgs returns the connection URL with its password removed, plus PGPASSWORD for the
// child's environment (P3-4): command lines are world-readable via ps and /proc.
func pgConnArgs(databaseURL string) (string, []string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", nil, fmt.Errorf("invalid database URL")
	}
	var env []string
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	return u.String(), env, nil
}

// BackupDatabase streams `pg_dump -Fc` into <dir>/backup_<ts>.dump[.age]. With recipients the
// stream is age-encrypted before it touches the disk. The file is written under a temp name and
// renamed, then the retention policy is applied.
func BackupDatabase(databaseURL, backupDir string, opts BackupOptions) (string, error) {
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}
	connURL, env, err := pgConnArgs(databaseURL)
	if err != nil {
		return "", err
	}

	ext := plainExt
	if len(opts.Recipients) > 0 {
		ext = encryptedExt
	} else {
		slog.Warn("BACKUP_AGE_RECIPIENTS not set: writing an UNENCRYPTED database backup")
	}
	final := filepath.Join(backupDir, backupPrefix+time.Now().Format(backupTimeLayout)+ext)
	tmp := final + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to create backup file: %w", err)
	}
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}

	var sink io.Writer = f
	var enc io.WriteCloser
	if len(opts.Recipients) > 0 {
		enc, err = age.Encrypt(f, opts.Recipients...)
		if err != nil {
			return fail(fmt.Errorf("failed to start encryption: %w", err))
		}
		sink = enc
	}

	// -Fc custom format (compressed); written to stdout so it can be encrypted in-stream.
	if err := runCommand(context.Background(), "pg_dump", []string{"-d", connURL, "-F", "c", "-Z", "5"}, env, nil, sink); err != nil {
		return fail(err)
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return fail(fmt.Errorf("failed to finish encryption: %w", err))
		}
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("failed to sync backup: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("failed to close backup: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("failed to finalize backup: %w", err)
	}
	slog.Info("Backup created successfully", "path", final, "encrypted", enc != nil)

	if removed, err := ApplyRetention(backupDir, opts.Retention, time.Now()); err != nil {
		slog.Error("Failed to apply backup retention", "error", err)
	} else if len(removed) > 0 {
		slog.Info("Pruned old backups", "count", len(removed))
	}
	return final, nil
}

// RestoreDatabase restores a backup with `pg_restore -c --if-exists`. Encrypted archives are
// decrypted in-stream into pg_restore's stdin (never written to disk in plaintext).
func RestoreDatabase(databaseURL, backupPath string, opts RestoreOptions) error {
	f, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("backup file does not exist: %s", filepath.Base(backupPath))
	}
	defer f.Close()

	connURL, env, err := pgConnArgs(databaseURL)
	if err != nil {
		return err
	}

	var in io.Reader = f
	if strings.HasSuffix(backupPath, encryptedExt) {
		if len(opts.Identities) == 0 {
			return fmt.Errorf("backup is encrypted: set BACKUP_AGE_IDENTITY_FILE to the age private key to restore")
		}
		dec, err := age.Decrypt(bufio.NewReader(f), opts.Identities...)
		if err != nil {
			return fmt.Errorf("failed to decrypt backup: %w", err)
		}
		in = dec
	}

	// -c drops objects before recreating them; --if-exists avoids errors for missing objects.
	// No file argument: the archive is read from stdin (works for custom format, sequentially).
	if err := runCommand(context.Background(), "pg_restore", []string{"-c", "--if-exists", "-d", connURL}, env, in, io.Discard); err != nil {
		return err
	}
	slog.Info("Database restored successfully", "path", backupPath)
	return nil
}

// IsBackupFile reports whether name is a backup produced by BackupDatabase (.dump or .dump.age).
func IsBackupFile(name string) bool { return isBackupFile(name) }

func isBackupFile(name string) bool {
	return strings.HasPrefix(name, backupPrefix) && (strings.HasSuffix(name, plainExt) || strings.HasSuffix(name, encryptedExt))
}

// parseBackupTime extracts the timestamp from backup_<ts>.dump[.age].
func parseBackupTime(name string) (time.Time, bool) {
	if !isBackupFile(name) {
		return time.Time{}, false
	}
	stamp := strings.TrimPrefix(name, backupPrefix)
	stamp = strings.TrimSuffix(strings.TrimSuffix(stamp, ".age"), plainExt)
	ts, err := time.ParseInLocation(backupTimeLayout, stamp, time.Local)
	return ts, err == nil
}

// GetLatestBackup returns the path to the most recent backup file (encrypted or not).
func GetLatestBackup(backupDir string) (string, error) {
	files, err := os.ReadDir(backupDir)
	if err != nil {
		return "", fmt.Errorf("failed to read backup directory: %w", err)
	}
	var latest string
	var latestTS time.Time
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if ts, ok := parseBackupTime(f.Name()); ok && (latest == "" || ts.After(latestTS)) {
			latest, latestTS = f.Name(), ts
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no backup files found in %s", backupDir)
	}
	return filepath.Join(backupDir, latest), nil
}

// selectBackupsToKeep applies the retention policy to backup file names.
func selectBackupsToKeep(names []string, p RetentionPolicy, now time.Time) map[string]bool {
	type entry struct {
		name string
		ts   time.Time
	}
	var all []entry
	for _, n := range names {
		if ts, ok := parseBackupTime(n); ok {
			all = append(all, entry{n, ts})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts.After(all[j].ts) }) // newest first

	keep := map[string]bool{}
	for i := 0; i < len(all) && i < p.KeepLatest; i++ {
		keep[all[i].name] = true
	}
	days := map[string]bool{}
	weeks := map[string]bool{}
	for _, e := range all { // newest first → first seen per bucket is the newest
		age := now.Sub(e.ts)
		dayKey := e.ts.Format("2006-01-02")
		if age < time.Duration(p.Daily)*24*time.Hour && !days[dayKey] && len(days) < p.Daily {
			days[dayKey] = true
			keep[e.name] = true
		}
		y, w := e.ts.ISOWeek()
		weekKey := fmt.Sprintf("%d-W%02d", y, w)
		if age < time.Duration(p.Weekly)*7*24*time.Hour && !weeks[weekKey] && len(weeks) < p.Weekly {
			weeks[weekKey] = true
			keep[e.name] = true
		}
	}
	return keep
}

// ApplyRetention deletes backups the policy doesn't keep and returns their names. Files that are
// not backups (by name) are never touched.
func ApplyRetention(backupDir string, p RetentionPolicy, now time.Time) ([]string, error) {
	files, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		if !f.IsDir() && isBackupFile(f.Name()) {
			names = append(names, f.Name())
		}
	}
	keep := selectBackupsToKeep(names, p, now)
	var removed []string
	for _, n := range names {
		if keep[n] {
			continue
		}
		if err := os.Remove(filepath.Join(backupDir, n)); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}
