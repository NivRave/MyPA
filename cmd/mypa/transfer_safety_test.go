package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// populatedTarget returns an install that already holds a household, with the source's backup
// copied into its backup directory.
func populatedTarget(t *testing.T, source *install) *install {
	t.Helper()
	target := newInstall(t, source.identity)
	require.NoError(t, db.Migrate(context.Background(), target.sqlDB))
	require.NoError(t, target.client.UpsertUser(models.User{PlatformID: "9001", Name: "Existing owner", Role: "admin", FamilyGroup: "Cohen"}))
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	return target
}

func backupFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "backup_*"))
	require.NoError(t, err)
	return matches
}

// With --replace the import empties the database. A backup that this install's backup key does
// not open must be found out before that, with the database exactly as it was and without even a
// safety backup having been needed.
func TestTransfer_AWrongBackupKeyChangesNothing(t *testing.T) {
	source := newInstall(t, newIdentity(t))
	source.seed()
	code, report := source.run("export")
	require.Zero(t, code, report)

	target := populatedTarget(t, source)
	manifest := findManifest(t, target.deps.BackupDir)
	before := backupFilesIn(t, target.deps.BackupDir)
	wrongKey := newIdentity(t)
	target.deps.Restore.Identities[0] = wrongKey

	code, report = target.run("import", manifest, "--replace")

	assert.Equal(t, 1, code)
	assert.Contains(t, report, "nothing was changed")
	assert.Contains(t, report, "backup key does not open")
	assert.Equal(t, []string{"Existing owner"}, target.userNames(), "the database must be exactly as it was")
	assert.Equal(t, before, backupFilesIn(t, target.deps.BackupDir), "no backup is taken for an import that never starts")
	current, latest, err := db.SchemaVersion(context.Background(), target.sqlDB)
	require.NoError(t, err)
	assert.Equal(t, latest, current)
}

// A dump without a manifest has no checksum. One that was cut short in a copy must still be found
// out before the database is emptied: the real pg_restore reads it to its end first.
func TestTransfer_ATruncatedDumpChangesNothing(t *testing.T) {
	source := newInstall(t, nil)
	source.seed()
	dump, err := db.BackupDatabase(source.deps.DatabaseURL, t.TempDir(), db.BackupOptions{Retention: db.DefaultRetention, Run: pgtest.ToolRunner()})
	require.NoError(t, err)
	whole, err := os.ReadFile(dump)
	require.NoError(t, err)

	target := newInstall(t, nil)
	require.NoError(t, db.Migrate(context.Background(), target.sqlDB))
	require.NoError(t, target.client.UpsertUser(models.User{PlatformID: "9001", Name: "Existing owner", Role: "admin"}))
	name := "backup_2026-11-18_21-52-46.dump"
	require.NoError(t, os.WriteFile(filepath.Join(target.deps.BackupDir, name), whole[:len(whole)*6/10], 0o600))

	code, report := target.run("import", name, "--replace")

	assert.Equal(t, 1, code)
	assert.Contains(t, report, "nothing was changed")
	assert.Contains(t, report, "cannot be read to its end")
	assert.Equal(t, []string{"Existing owner"}, target.userNames())

	// The same dump, whole, restores.
	require.NoError(t, os.WriteFile(filepath.Join(target.deps.BackupDir, name), whole, 0o600))
	code, report = target.run("import", name, "--replace")
	require.Zero(t, code, report)
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
}

// A different machine usually means a different database role. A backup taken under one role
// restores under another, and the restoring role owns everything afterwards.
func TestTransfer_RestoresUnderAnotherDatabaseRole(t *testing.T) {
	identity := newIdentity(t)
	source := newInstall(t, identity)
	source.seed()
	code, report := source.run("export")
	require.Zero(t, code, report)

	target := newInstall(t, identity)
	// Point the target at a database owned by, and reached as, another role.
	otherDSN := pgtest.NewDatabaseOwnedBy(t, "newmachine")
	client, err := db.Open(otherDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	sqlDB, err := client.DB.DB()
	require.NoError(t, err)
	target.client, target.sqlDB, target.deps.DatabaseURL = client, sqlDB, otherDSN
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)

	code, report = target.run("import", findManifest(t, target.deps.BackupDir))
	require.Zero(t, code, report)

	var connectedAs string
	require.NoError(t, sqlDB.QueryRow(`SELECT current_user`).Scan(&connectedAs))
	require.Equal(t, "newmachine", connectedAs)
	rows, err := sqlDB.Query(`SELECT tablename, tableowner FROM pg_tables WHERE schemaname = 'public'`)
	require.NoError(t, err)
	defer rows.Close()
	tables := 0
	for rows.Next() {
		var table, owner string
		require.NoError(t, rows.Scan(&table, &owner))
		assert.Equal(t, "newmachine", owner, "table %s must belong to the role that restored it, not the role that dumped it", table)
		tables++
	}
	require.NoError(t, rows.Err())
	assert.GreaterOrEqual(t, tables, 29)
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
	assert.Equal(t, tableCounts(t, source.sqlDB), tableCounts(t, sqlDB), "every row of every table arrives")
	assert.JSONEq(t, danaToken, target.googleSignIn("1001"))
}

// By default the key file is locked with the backup key: nothing secret is printed, and it opens
// on the new install with the private backup key the operator brings anyway.
func TestTransfer_KeysLockedWithTheBackupKey(t *testing.T) {
	identity := newIdentity(t)
	source := newInstall(t, identity)
	source.deps.EncryptionKey = "the-old-install-key-0123456789abcdef"
	source.deps.BackupRecipients = identity.Recipient().String()

	code, report := source.run("export-keys")
	require.Zero(t, code, report)
	assert.Contains(t, report, "locked with the backup key")
	assert.NotContains(t, report, source.deps.EncryptionKey)
	assert.NotRegexp(t, `([A-Z2-7]{4}-){7}[A-Z2-7]{4}`, report, "no passphrase is involved")
	raw, err := os.ReadFile(filepath.Join(source.deps.BackupDir, "mypa-keys.age"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), source.deps.EncryptionKey)

	// A machine that has the file but not the private backup key gets nothing.
	thief := newInstall(t, newIdentity(t))
	copySet(t, source.deps.BackupDir, thief.deps.BackupDir)
	code, report = thief.run("import-keys")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "the backup key does not open this key file")
	keyless := newInstall(t, nil)
	copySet(t, source.deps.BackupDir, keyless.deps.BackupDir)
	code, report = keyless.run("import-keys")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "BACKUP_AGE_IDENTITY_FILE")

	// The new install, with the private backup key.
	target := newInstall(t, identity)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	code, report = target.run("import-keys")
	require.Zero(t, code, report)
	assert.NotContains(t, report, "the-old-install-key")
	env, err := os.ReadFile(filepath.Join(target.deps.BackupDir, "mypa-keys.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "ENCRYPTION_KEY=the-old-install-key-0123456789abcdef\n")

	// An install whose backups are not encrypted has no backup key to lock the file with.
	plain := newInstall(t, nil)
	plain.deps.EncryptionKey = "k"
	code, report = plain.run("export-keys")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "--passphrase")
	assert.Empty(t, backupFilesIn(t, plain.deps.BackupDir))
	_, statErr := os.Stat(filepath.Join(plain.deps.BackupDir, "mypa-keys.age"))
	assert.True(t, os.IsNotExist(statErr), "a refused export writes no file")
}
