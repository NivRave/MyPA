package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/nivik/mypa/internal/transfer"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests run the restore-and-migrate tool end to end (V4-43): a real Postgres of the
// production version, the real pg_dump and pg_restore (run inside that container), the real
// migrations, and the same code path the `transfer` command uses. Only RabbitMQ is a function.

var updateBaseline = flag.Bool("update-baseline", false, "regenerate testdata/baseline_v3.4.0.dump (only valid while the schema is still the v3.4.0 one)")

// baselineSchemaVersion is the migration version of release v3.4.0, the pre-V4 baseline (OP-10).
const baselineSchemaVersion = 2

const (
	danaToken = `{"access_token":"dana","refresh_token":"dana-refresh","token_type":"Bearer"}`
	omerToken = `{"access_token":"omer","refresh_token":"omer-refresh","token_type":"Bearer"}`
)

// install is one MyPA install as the tool sees it: a database, a cache, a backup directory and
// an archive directory.
type install struct {
	t        *testing.T
	deps     transferDeps
	out      *bytes.Buffer
	client   *db.Client
	sqlDB    *sql.DB
	store    *state.Store
	identity *age.X25519Identity
}

// newInstall creates an install with an empty database. queues is the state its queues report.
func newInstall(t *testing.T, identity *age.X25519Identity) *install {
	t.Helper()
	dsn := pgtest.NewDatabase(t)
	client, err := db.Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	sqlDB, err := client.DB.DB()
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	out := &bytes.Buffer{}
	i := &install{t: t, out: out, client: client, sqlDB: sqlDB, store: store, identity: identity}
	i.deps = transferDeps{
		DatabaseURL: dsn,
		BackupDir:   t.TempDir(),
		ArchiveDir:  filepath.Join(t.TempDir(), "archives"),
		Backup:      db.BackupOptions{Retention: db.DefaultRetention, Run: pgtest.ToolRunner()},
		Restore:     db.RestoreOptions{Run: pgtest.ToolRunner()},
		Probe:       func(context.Context, string) (int, int, error) { return 0, 0, nil }, // stopped and drained
		Store:       store,
		Out:         out,
	}
	if identity != nil {
		i.deps.Backup.Recipients = []age.Recipient{identity.Recipient()}
		i.deps.Restore.Identities = []age.Identity{identity}
	}
	return i
}

// run executes one transfer command and returns its exit code and what it printed.
func (i *install) run(args ...string) (int, string) {
	i.t.Helper()
	i.out.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	code := runTransfer(ctx, args, i.deps)
	return code, i.out.String()
}

// seed fills a migrated database with a small household: two members, one Google sign-in copy in
// the database, a memory with an embedding and a reminder.
func (i *install) seed() {
	i.t.Helper()
	require.NoError(i.t, db.Migrate(context.Background(), i.sqlDB))
	for _, u := range []models.User{
		{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"},
		{PlatformID: "1002", Name: "Omer", Role: "family", FamilyGroup: "Levi"},
	} {
		require.NoError(i.t, i.client.UpsertUser(u))
	}
	ciphertext, err := crypto.Encrypt([]byte(danaToken), "")
	require.NoError(i.t, err)
	require.NoError(i.t, i.client.SaveEncryptedCredential(models.EncryptedCredential{UserID: "1001", Provider: googleProvider, Ciphertext: ciphertext}))

	embedding := make([]float32, 3072)
	embedding[0], embedding[7] = 1, 0.5
	require.NoError(i.t, i.client.DB.Create(&models.TopicMemory{
		UserID: "1001", FamilyGroup: "Levi", Topic: "house", TagsJSON: "[]", Scope: "family",
		Fact: "The boiler is serviced every November.", CreatedByUserID: "1001", ValidFrom: time.Now(),
		Embedding: pgvector.NewVector(embedding),
	}).Error)
	require.NoError(i.t, i.client.SaveReminder(models.ScheduledReminder{UserID: "1002", Message: "Call the plumber", DueTime: time.Now().Add(48 * time.Hour)}))
}

func (i *install) userNames() []string {
	i.t.Helper()
	var names []string
	require.NoError(i.t, i.client.DB.Model(&models.User{}).Order("platform_id").Pluck("name", &names).Error)
	return names
}

// googleSignIn reads a member's Google sign-in the way the running app does: from the cache,
// falling back to the encrypted copy in the database (the wiring in main.go).
func (i *install) googleSignIn(userID string) string {
	i.t.Helper()
	module := durability.NewModule(i.client)
	i.store.SetCredentialFallback(func(ctx context.Context, id string) ([]byte, error) {
		return module.GetCredentialFallback(ctx, id, googleProvider)
	})
	token, err := i.store.GetOAuthToken(context.Background(), userID)
	require.NoError(i.t, err)
	return string(token)
}

func writeArchive(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// copySet copies every file of a backup directory to another one: the backup travels.
func copySet(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	require.NoError(t, err)
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(from, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(to, e.Name()), body, 0o600))
	}
}

func hashDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

func findManifest(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "backup_*.manifest.json"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "expected exactly one backup set in %s", dir)
	return filepath.Base(matches[0])
}

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return id
}

// V4-43: an install is moved to another machine. The database, the archive files and the Google
// sign-ins arrive; the schema is current; the backup that travelled is untouched.
func TestTransfer_MoveAnInstall(t *testing.T) {
	identity := newIdentity(t)
	source := newInstall(t, identity)
	source.seed()
	writeArchive(t, source.deps.ArchiveDir, "audit_archive_1700000000.jsonl.gz", "conversations of last winter")
	writeArchive(t, source.deps.ArchiveDir, "audit_archive_1700086400.jsonl.gz", "conversations of last spring")
	// Omer connected Google before database copies existed: his sign-in is in the cache only.
	require.NoError(t, source.store.SetOAuthToken(context.Background(), "1002", []byte(omerToken)))

	code, report := source.run("export")
	require.Zero(t, code, report)
	assert.Contains(t, report, "Google sign-ins in the database: 2 (1 copied from the cache just now)")
	manifest := findManifest(t, source.deps.BackupDir)

	code, report = source.run("verify", manifest)
	require.Zero(t, code, report)
	assert.Contains(t, report, "2 archive file(s), encrypted: true")

	// The new machine: an empty database, an empty cache, no archive files. The backup is copied.
	target := newInstall(t, identity)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	travelled := hashDir(t, target.deps.BackupDir)

	code, report = target.run("import", manifest)
	require.Zero(t, code, report)
	t.Log("\n" + report)

	// The data arrived.
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
	var fact string
	require.NoError(t, target.client.DB.Model(&models.TopicMemory{}).Where("user_id = ?", "1001").Pluck("fact", &fact).Error)
	assert.Equal(t, "The boiler is serviced every November.", fact)
	var dims int
	require.NoError(t, target.sqlDB.QueryRow(`SELECT vector_dims(embedding) FROM topic_memories LIMIT 1`).Scan(&dims))
	assert.Equal(t, 3072, dims, "embeddings survive the move")
	due, err := target.client.GetDueReminders()
	require.NoError(t, err)
	assert.Empty(t, due, "the reminder is not due yet, but it must exist")
	var reminders int64
	require.NoError(t, target.client.DB.Model(&models.ScheduledReminder{}).Count(&reminders).Error)
	assert.Equal(t, int64(1), reminders)

	// Google sign-ins are intact, including the one that lived only in the old cache.
	assert.JSONEq(t, danaToken, target.googleSignIn("1001"))
	assert.JSONEq(t, omerToken, target.googleSignIn("1002"))
	assert.Contains(t, report, "Google sign-ins carried over: 2")

	// The schema is current, and /doctor's schema check agrees.
	current, latest, err := db.SchemaVersion(context.Background(), target.sqlDB)
	require.NoError(t, err)
	assert.Equal(t, latest, current)
	check := doctor.Schema{Version: func(ctx context.Context) (int64, int64, error) { return db.SchemaVersion(ctx, target.sqlDB) }}
	results := check.Run(context.Background(), doctor.Scope{})
	require.Len(t, results, 1)
	assert.Equal(t, doctor.StatusOK, results[0].Status, results[0].Detail)

	// The archive files arrived.
	body, err := os.ReadFile(filepath.Join(target.deps.ArchiveDir, "audit_archive_1700086400.jsonl.gz"))
	require.NoError(t, err)
	assert.Equal(t, "conversations of last spring", string(body))
	assert.Contains(t, report, "Archive files: 2 restored, 0 already in place")

	// The report says what did not travel and what to do next.
	assert.Contains(t, report, "Not carried by a backup")
	assert.Contains(t, report, "/doctor")

	// The backup itself is unchanged: it remains the rollback.
	assert.Equal(t, travelled, hashDir(t, target.deps.BackupDir))

	// Importing again into the now populated install is refused.
	code, report = target.run("import", manifest)
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "already holds users")
}

// A restore never replaces data silently, and when it is told to replace, the previous database
// is backed up first and can be brought back.
func TestTransfer_ReplaceIsExplicitAndReversible(t *testing.T) {
	source := newInstall(t, nil)
	source.deps.Backup.Recipients = nil
	source.seed()
	code, report := source.run("export", "--allow-unencrypted")
	require.Zero(t, code, report)
	manifest := findManifest(t, source.deps.BackupDir)

	// The target already has a different household.
	target := newInstall(t, nil)
	require.NoError(t, db.Migrate(context.Background(), target.sqlDB))
	require.NoError(t, target.client.UpsertUser(models.User{PlatformID: "9001", Name: "Existing owner", Role: "admin", FamilyGroup: "Cohen"}))
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)

	code, report = target.run("import", manifest)
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "--replace")
	assert.Equal(t, []string{"Existing owner"}, target.userNames(), "a refused import must change nothing")

	code, report = target.run("import", manifest, "--replace")
	require.Zero(t, code, report)
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
	safety := regexp.MustCompile(`Safety backup of the current database: (backup_\S+\.dump)`).FindStringSubmatch(report)
	require.Len(t, safety, 2, report)

	// The safety backup brings the previous database back.
	code, report = target.run("import", safety[1], "--replace")
	require.Zero(t, code, report)
	assert.Equal(t, []string{"Existing owner"}, target.userNames())
}

// A running target is refused before anything is touched.
func TestTransfer_ImportRefusesARunningInstall(t *testing.T) {
	source := newInstall(t, nil)
	source.seed()
	code, report := source.run("export", "--allow-unencrypted")
	require.Zero(t, code, report)

	target := newInstall(t, nil)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	target.deps.Probe = func(_ context.Context, queue string) (int, int, error) { return 0, 1, nil }

	code, report = target.run("import", findManifest(t, target.deps.BackupDir))
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "this install is running")
	exists, err := db.HasUserData(context.Background(), target.sqlDB)
	require.NoError(t, err)
	assert.False(t, exists)
}

// The data encryption key has to travel. Without it the restore still succeeds, but the tool
// says plainly that the stored secrets cannot be opened, and exits non-zero.
func TestTransfer_ImportReportsAMissingDataKey(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "the-old-install-key-0123456789abcdef")
	source := newInstall(t, nil)
	source.seed()
	code, report := source.run("export", "--allow-unencrypted")
	require.Zero(t, code, report)

	target := newInstall(t, nil)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	manifest := findManifest(t, target.deps.BackupDir)

	t.Setenv("ENCRYPTION_KEY", "a-freshly-generated-key-on-the-new-box")
	code, report = target.run("import", manifest)
	assert.Equal(t, 1, code, "a restore whose secrets cannot be opened is not finished")
	assert.Contains(t, report, "ENCRYPTION_KEY does not open 1 of 1 stored secret(s)")
	assert.Contains(t, report, "export-keys")
	assert.Contains(t, report, "NOT finished")
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames(), "the data itself is restored")

	// With the key brought over, the same restored data opens. No second import is needed.
	t.Setenv("ENCRYPTION_KEY", "the-old-install-key-0123456789abcdef")
	assert.JSONEq(t, danaToken, target.googleSignIn("1001"))
}

// The keys travel through export-keys and import-keys, protected by a generated passphrase.
func TestTransfer_KeysTravelProtected(t *testing.T) {
	identity := newIdentity(t)
	identityFile := filepath.Join(t.TempDir(), "backup-key.txt")
	require.NoError(t, os.WriteFile(identityFile, []byte(identity.String()+"\n"), 0o600))

	source := newInstall(t, identity)
	source.deps.EncryptionKey = "the-old-install-key-0123456789abcdef"
	source.deps.BackupRecipients = identity.Recipient().String()
	source.deps.IdentityFile = identityFile

	code, report := source.run("export-keys", "--with-backup-key")
	require.Zero(t, code, report)
	passphrase := regexp.MustCompile(`(?m)^\s+((?:[A-Z2-7]{4}-){7}[A-Z2-7]{4})\s*$`).FindStringSubmatch(report)
	require.Len(t, passphrase, 2, "the passphrase must be shown:\n%s", report)
	keyFile := filepath.Join(source.deps.BackupDir, "mypa-keys.age")
	raw, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), source.deps.EncryptionKey)
	assert.NotContains(t, string(raw), "AGE-SECRET-KEY")
	assert.NotContains(t, report, source.deps.EncryptionKey, "the key itself is never printed")

	// A second export does not overwrite the first.
	code, report = source.run("export-keys")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "already exists")

	// On the new install.
	target := newInstall(t, identity)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	passFile := filepath.Join(t.TempDir(), "passphrase.txt")

	require.NoError(t, os.WriteFile(passFile, []byte("AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA\n"), 0o600))
	code, report = target.run("import-keys", "--passphrase-file", passFile)
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "does not open this key file")

	code, report = target.run("import-keys")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "--passphrase-file")

	require.NoError(t, os.WriteFile(passFile, []byte(strings.ToLower(passphrase[1])+"\n"), 0o600))
	code, report = target.run("import-keys", "--passphrase-file", passFile)
	require.Zero(t, code, report)
	env, err := os.ReadFile(filepath.Join(target.deps.BackupDir, "mypa-keys.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "ENCRYPTION_KEY=the-old-install-key-0123456789abcdef\n")
	assert.Contains(t, string(env), "BACKUP_AGE_RECIPIENTS="+identity.Recipient().String()+"\n")
	backupKey, err := os.ReadFile(filepath.Join(target.deps.BackupDir, "mypa-backup-key.txt"))
	require.NoError(t, err)
	assert.Equal(t, identity.String()+"\n", string(backupKey))
	assert.NotContains(t, report, "the-old-install-key", "secrets go to files, never to the terminal")

	// Both files were opened by passing through transfer.ImportKeys; make sure the round trip
	// matches what the library reports directly.
	f, err := os.Open(keyFile)
	require.NoError(t, err)
	defer f.Close()
	keys, err := transfer.ImportKeys(f, passphrase[1])
	require.NoError(t, err)
	assert.Equal(t, source.deps.EncryptionKey, keys.EncryptionKey)
}

// The upgrade path from the pre-V4 baseline (OP-10, DE-10): a database dump taken by release
// v3.4.0 restores into this build and the migrations bring it up to date. The dump is a committed
// fixture, so every migration added during V4 is exercised against real v3.4.0 data.
func TestTransfer_BaselineDumpRestoresAndMigrates(t *testing.T) {
	fixture := filepath.Join("testdata", "baseline_v3.4.0.dump")
	if *updateBaseline {
		writeBaselineFixture(t, fixture)
	}
	body, err := os.ReadFile(fixture)
	require.NoError(t, err, "the baseline fixture is missing")

	// A baseline backup is what v3.4.0 wrote: one age-encrypted dump, named by its time, with
	// no manifest and no archive files next to it.
	identity := newIdentity(t)
	target := newInstall(t, identity)
	name := "backup_2026-11-18_21-52-46.dump.age"
	var encrypted bytes.Buffer
	enc, err := age.Encrypt(&encrypted, identity.Recipient())
	require.NoError(t, err)
	_, err = enc.Write(body)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	require.NoError(t, os.WriteFile(filepath.Join(target.deps.BackupDir, name), encrypted.Bytes(), 0o600))

	// Without the backup key nothing happens.
	withKey := target.deps
	target.deps.Restore.Identities = nil
	code, report := target.run("import", name)
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "BACKUP_AGE_IDENTITY_FILE")
	target.deps = withKey

	code, report = target.run("import", name)
	require.Zero(t, code, report)
	t.Log("\n" + report)

	assert.Contains(t, report, "without a manifest")
	current, latest, err := db.SchemaVersion(context.Background(), target.sqlDB)
	require.NoError(t, err)
	assert.Equal(t, latest, current, "the baseline must be migrated to this build's schema")
	assert.GreaterOrEqual(t, latest, int64(baselineSchemaVersion))

	// Every feature's data that the baseline held is still there.
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
	assert.JSONEq(t, danaToken, target.googleSignIn("1001"), "Google sign-ins from the baseline stay usable")
	var facts int64
	require.NoError(t, target.client.DB.Model(&models.TopicMemory{}).Count(&facts).Error)
	assert.Equal(t, int64(1), facts)
}

// writeBaselineFixture dumps a seeded database at the v3.4.0 schema. It refuses to run once the
// schema has moved on: from then on the fixture can only be produced from the v3.4.0 tag.
func writeBaselineFixture(t *testing.T, path string) {
	t.Helper()
	source := newInstall(t, nil)
	source.seed()
	current, latest, err := db.SchemaVersion(context.Background(), source.sqlDB)
	require.NoError(t, err)
	require.Equal(t, int64(baselineSchemaVersion), latest,
		"this build's schema is no longer the v3.4.0 one, so it cannot produce the baseline fixture; generate it from the v3.4.0 tag")
	require.Equal(t, latest, current)

	dir := t.TempDir()
	dump, err := db.BackupDatabase(source.deps.DatabaseURL, dir, db.BackupOptions{Retention: db.DefaultRetention, Run: pgtest.ToolRunner()})
	require.NoError(t, err)
	body, err := os.ReadFile(dump)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, body, 0o644))
	t.Logf("wrote %s (%d bytes)", path, len(body))
}

func TestTransfer_UsageAndUnknownCommands(t *testing.T) {
	i := newInstall(t, nil)
	code, report := i.run("frobnicate")
	assert.Equal(t, 2, code)
	assert.Contains(t, report, "unknown command")
	assert.Contains(t, report, "Usage: orchestrator transfer")

	code, report = i.run("import")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "name the backup to use")

	code, report = i.run("import", "notes.txt")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "neither a backup manifest")
}

// Without the cache, an export cannot know whether a Google sign-in exists only there, so it
// refuses unless the operator says the cache is lost.
func TestTransfer_ExportNeedsTheCacheUnlessToldOtherwise(t *testing.T) {
	source := newInstall(t, nil)
	source.seed()
	source.deps.Store = nil

	code, report := source.run("export", "--allow-unencrypted")
	assert.Equal(t, 1, code)
	assert.Contains(t, report, "--skip-sign-in-check")
	matches, err := filepath.Glob(filepath.Join(source.deps.BackupDir, "backup_*"))
	require.NoError(t, err)
	assert.Empty(t, matches)

	code, report = source.run("export", "--allow-unencrypted", "--skip-sign-in-check")
	require.Zero(t, code, report)
	assert.Contains(t, report, "Google sign-in check skipped")
}

// Rollback: the backup taken before an upgrade is restored as it was, without migrating, so the
// release that took it can be started on it again.
func TestTransfer_RollbackRestoresWithoutMigrating(t *testing.T) {
	source := newInstall(t, nil)
	source.seed()
	code, report := source.run("export", "--allow-unencrypted")
	require.Zero(t, code, report)

	target := newInstall(t, nil)
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)

	code, report = target.run("import", findManifest(t, target.deps.BackupDir), "--no-migrate")
	require.Zero(t, code, report)
	assert.Contains(t, report, "left at schema version 2, as asked")
	assert.Contains(t, report, "Do not start this release on this database")
	assert.NotContains(t, report, "docker compose up")
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
}

// Restoring an old backup over a populated install takes a safety backup first. That backup must
// not trigger retention: with enough newer backups in the directory, pruning would delete the
// set that is being restored, after the database has already been emptied.
func TestTransfer_SafetyBackupDoesNotPruneTheSetBeingRestored(t *testing.T) {
	source := newInstall(t, nil)
	source.seed()
	code, report := source.run("export", "--allow-unencrypted")
	require.Zero(t, code, report)

	target := newInstall(t, nil)
	require.NoError(t, db.Migrate(context.Background(), target.sqlDB))
	require.NoError(t, target.client.UpsertUser(models.User{PlatformID: "9001", Name: "Existing owner", Role: "admin"}))
	copySet(t, source.deps.BackupDir, target.deps.BackupDir)
	manifest := findManifest(t, target.deps.BackupDir)
	// Many newer backups: under the retention policy the set to restore is now the oldest and
	// would be pruned by any backup that applies retention.
	for day := 1; day <= 40; day++ {
		name := "backup_" + time.Now().Add(time.Duration(day)*24*time.Hour).Format("2006-01-02_15-04-05") + ".dump"
		require.NoError(t, os.WriteFile(filepath.Join(target.deps.BackupDir, name), []byte("newer"), 0o600))
	}

	code, report = target.run("import", manifest, "--replace")
	require.Zero(t, code, report)
	assert.Equal(t, []string{"Dana", "Omer"}, target.userNames())
	_, err := db.VerifyBackupSet(filepath.Join(target.deps.BackupDir, manifest))
	require.NoError(t, err, "the restored set must still be there, intact")
}
