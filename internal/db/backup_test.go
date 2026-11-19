package db

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner records invocations and plays pg_dump / pg_restore.
type fakeRunner struct {
	calls    [][]string
	envs     [][]string
	dumpData []byte
	restored []byte
}

func (f *fakeRunner) run(_ context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.envs = append(f.envs, env)
	switch name {
	case "pg_dump":
		_, err := stdout.Write(f.dumpData)
		return err
	case "pg_restore":
		b, err := io.ReadAll(stdin)
		f.restored = b
		return err
	}
	return fmt.Errorf("unexpected command %s", name)
}

func useFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{dumpData: []byte("PGDMP-custom-format-archive-bytes")}
	prev := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = prev })
	return f
}

const testDBURL = "postgres://mypa:s3cr3t-pw@postgres:5432/mypa?sslmode=disable"

// P3-4: the database password travels in PGPASSWORD, never in argv (visible via ps / /proc).
func TestPgConnArgs_PasswordMovesToEnv(t *testing.T) {
	url, env, err := pgConnArgs(testDBURL)
	require.NoError(t, err)
	assert.NotContains(t, url, "s3cr3t-pw")
	assert.Contains(t, url, "mypa@postgres:5432/mypa")
	assert.Contains(t, env, "PGPASSWORD=s3cr3t-pw")

	url, env, err = pgConnArgs("postgres://mypa@postgres:5432/mypa")
	require.NoError(t, err)
	assert.Equal(t, "postgres://mypa@postgres:5432/mypa", url)
	assert.Empty(t, env)
}

func TestBackup_EncryptedAtRest_RoundTrip(t *testing.T) {
	f := useFakeRunner(t)
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	dir := t.TempDir()

	path, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, ".dump.age"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(raw, f.dumpData), "no plaintext on disk")

	// argv never carries the password.
	for _, c := range f.calls {
		assert.NotContains(t, strings.Join(c, " "), "s3cr3t-pw")
	}
	assert.Contains(t, f.envs[0], "PGPASSWORD=s3cr3t-pw")

	// Restore decrypts into pg_restore's stdin.
	require.NoError(t, RestoreDatabase(testDBURL, path, RestoreOptions{Identities: []age.Identity{id}}))
	assert.Equal(t, f.dumpData, f.restored)
	last := strings.Join(f.calls[len(f.calls)-1], " ")
	assert.NotContains(t, last, "s3cr3t-pw")
	assert.NotContains(t, last, path, "encrypted archives are streamed via stdin, not passed as a path")
}

func TestRestore_EncryptedWithoutIdentity_Fails(t *testing.T) {
	useFakeRunner(t)
	id, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	path, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}})
	require.NoError(t, err)

	err = RestoreDatabase(testDBURL, path, RestoreOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BACKUP_AGE_IDENTITY_FILE")
}

// Without a configured key, backups still run (unencrypted) — a missing backup is worse.
func TestBackup_UnencryptedWhenNoRecipients(t *testing.T) {
	f := useFakeRunner(t)
	dir := t.TempDir()
	path, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, ".dump"))
	raw, _ := os.ReadFile(path)
	assert.Equal(t, f.dumpData, raw)

	require.NoError(t, RestoreDatabase(testDBURL, path, RestoreOptions{}))
	assert.Equal(t, f.dumpData, f.restored)
}

// A failed dump leaves no partial file behind.
func TestBackup_FailureLeavesNoFile(t *testing.T) {
	prev := runCommand
	runCommand = func(context.Context, string, []string, []string, io.Reader, io.Writer) error {
		return fmt.Errorf("pg_dump: connection refused")
	}
	t.Cleanup(func() { runCommand = prev })
	dir := t.TempDir()
	_, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention})
	require.Error(t, err)
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}

func backupName(ts time.Time, encrypted bool) string {
	n := "backup_" + ts.Format(backupTimeLayout) + ".dump"
	if encrypted {
		n += ".age"
	}
	return n
}

// P3-4 retention: latest 3 always, plus newest per day for 7 days, plus newest per ISO week for 4 weeks.
func TestRetention_GrandfatherFatherSon(t *testing.T) {
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.Local)
	var names []string
	for d := 0; d < 60; d++ { // two backups a day for 60 days
		day := now.AddDate(0, 0, -d)
		names = append(names, backupName(time.Date(day.Year(), day.Month(), day.Day(), 2, 0, 0, 0, time.Local), d%2 == 0))
		if d > 0 { // today's 14:00 run hasn't happened yet at 03:00
			names = append(names, backupName(time.Date(day.Year(), day.Month(), day.Day(), 14, 0, 0, 0, time.Local), false))
		}
	}
	keep := selectBackupsToKeep(names, DefaultRetention, now)

	var kept []string
	for n := range keep {
		kept = append(kept, n)
	}
	sort.Strings(kept)
	// Latest-3 rule: today 02:00 plus BOTH of yesterday's backups survive (the daily rule alone
	// would keep only yesterday 14:00 — this is what protects the file a restore is about to read
	// next to its same-day safety backup, P1-5).
	assert.True(t, keep[backupName(time.Date(2026, 11, 16, 2, 0, 0, 0, time.Local), true)])
	assert.True(t, keep[backupName(time.Date(2026, 11, 15, 14, 0, 0, 0, time.Local), false)])
	assert.True(t, keep[backupName(time.Date(2026, 11, 15, 2, 0, 0, 0, time.Local), false)])
	// Daily rule: an older same-day duplicate is pruned.
	assert.False(t, keep[backupName(time.Date(2026, 11, 12, 2, 0, 0, 0, time.Local), true)])
	assert.True(t, keep[backupName(time.Date(2026, 11, 12, 14, 0, 0, 0, time.Local), false)])
	// Nothing older than ~4 weeks + a bit survives.
	for _, n := range kept {
		ts, ok := parseBackupTime(n)
		require.True(t, ok, n)
		assert.True(t, now.Sub(ts) < 35*24*time.Hour, "kept %s is too old", n)
	}
	// Bounded count: ≤ 3 latest + 7 daily + 4 weekly.
	assert.LessOrEqual(t, len(kept), 14)
	assert.GreaterOrEqual(t, len(kept), 10)
}

func TestApplyRetention_DeletesOnlyPrunedBackups(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for d := 0; d < 40; d++ {
		ts := now.AddDate(0, 0, -d)
		require.NoError(t, os.WriteFile(filepath.Join(dir, backupName(ts, false)), []byte("x"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0o600))

	removed, err := ApplyRetention(dir, DefaultRetention, now)
	require.NoError(t, err)
	assert.NotEmpty(t, removed)
	_, err = os.Stat(filepath.Join(dir, "notes.txt"))
	assert.NoError(t, err, "non-backup files are never touched")
	latest, err := GetLatestBackup(dir)
	require.NoError(t, err)
	assert.Equal(t, backupName(now, false), filepath.Base(latest))
}

func TestGetLatestBackup_ConsidersEncryptedFiles(t *testing.T) {
	dir := t.TempDir()
	older := time.Date(2026, 11, 1, 2, 0, 0, 0, time.Local)
	newer := time.Date(2026, 11, 2, 2, 0, 0, 0, time.Local)
	require.NoError(t, os.WriteFile(filepath.Join(dir, backupName(older, false)), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, backupName(newer, true)), nil, 0o600))
	latest, err := GetLatestBackup(dir)
	require.NoError(t, err)
	assert.Equal(t, backupName(newer, true), filepath.Base(latest))
}

// BL-21: /restore tells "no backup" apart from an unreadable directory without showing the error
// text, which holds the directory path.
func TestGetLatestBackup_EmptyDirWrapsErrNoBackup(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
	_, err := GetLatestBackup(dir)
	require.ErrorIs(t, err, ErrNoBackup)

	_, err = GetLatestBackup(filepath.Join(dir, "missing"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoBackup, "an unreadable directory is not reported as an empty one")
}

func TestBackupOptionsFromEnv(t *testing.T) {
	id1, _ := age.GenerateX25519Identity()
	id2, _ := age.GenerateX25519Identity()
	t.Setenv("BACKUP_AGE_RECIPIENTS", id1.Recipient().String()+", "+id2.Recipient().String())
	opts, err := BackupOptionsFromEnv()
	require.NoError(t, err)
	assert.Len(t, opts.Recipients, 2)

	t.Setenv("BACKUP_AGE_RECIPIENTS", "not-a-key")
	_, err = BackupOptionsFromEnv()
	assert.Error(t, err)

	t.Setenv("BACKUP_AGE_RECIPIENTS", "")
	opts, err = BackupOptionsFromEnv()
	require.NoError(t, err)
	assert.Empty(t, opts.Recipients)
}

func TestRestoreOptionsFromEnv(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	keyFile := filepath.Join(t.TempDir(), "backup.key")
	require.NoError(t, os.WriteFile(keyFile, []byte("# comment\n"+id.String()+"\n"), 0o600))
	t.Setenv("BACKUP_AGE_IDENTITY_FILE", keyFile)
	opts, err := RestoreOptionsFromEnv()
	require.NoError(t, err)
	assert.Len(t, opts.Identities, 1)
}
