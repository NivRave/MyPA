package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/nivik/mypa/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDatabaseURL = "postgres://mypa:pw@postgres:5432/mypa?sslmode=disable"

// fakePostgresTools plays pg_dump and pg_restore.
func fakePostgresTools(_ context.Context, name string, _ []string, _ []string, stdin io.Reader, stdout io.Writer) error {
	if name == "pg_dump" {
		_, err := stdout.Write([]byte("PGDMP fake dump"))
		return err
	}
	_, err := io.Copy(io.Discard, stdin)
	return err
}

func queues(states map[string][2]int) QueueProbe {
	return func(_ context.Context, queue string) (int, int, error) {
		s := states[queue]
		return s[0], s[1], nil
	}
}

func newExporter(t *testing.T) (Exporter, *age.X25519Identity, *bytes.Buffer) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	archiveDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "audit_archive_1700000000.jsonl.gz"), []byte("archive"), 0o644))
	out := &bytes.Buffer{}
	return Exporter{
		DatabaseURL:   testDatabaseURL,
		BackupDir:     t.TempDir(),
		ArchiveDir:    archiveDir,
		Backup:        db.BackupOptions{Retention: db.DefaultRetention, Recipients: []age.Recipient{id.Recipient()}, Run: fakePostgresTools},
		Queues:        []string{"inbound.messages", "audit.events"},
		Probe:         queues(nil),
		SchemaVersion: func(context.Context) (int64, error) { return 2, nil },
		SyncSignIns: func(context.Context) (db.SignInInfo, error) {
			return db.SignInInfo{InDatabase: 2, CopiedNow: 1}, nil
		},
		Out: out,
	}, id, out
}

func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestExport_WritesAVerifiedSetThatRecordsItsSource(t *testing.T) {
	e, _, out := newExporter(t)

	m, manifestPath, err := e.Run(context.Background())
	require.NoError(t, err)

	onDisk, err := db.VerifyBackupSet(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, m, onDisk)
	assert.True(t, m.Encrypted)
	assert.Equal(t, int64(2), m.SchemaVersion)
	assert.Equal(t, 1, m.ArchiveFiles)
	require.NotNil(t, m.Source)
	assert.True(t, m.Source.Drained)
	assert.Equal(t, map[string]db.QueueInfo{"inbound.messages": {}, "audit.events": {}}, m.Source.Queues)
	assert.Equal(t, &db.SignInInfo{InDatabase: 2, CopiedNow: 1}, m.Source.GoogleSignIns)
	assert.Equal(t, NotCarried, m.NotCarried, "the manifest documents what a restore will not bring back")

	report := out.String()
	assert.Contains(t, report, "work queues are empty and nothing is consuming them")
	assert.Contains(t, report, "The gateway only publishes", "the report must not claim to have seen what it cannot see")
	assert.Contains(t, report, "Not carried by a backup")
	assert.Contains(t, report, "chat window")
	assert.Contains(t, report, "transfer import "+filepath.Base(manifestPath))
}

// V4-43: the procedure stops the old install first and drains the queue before the backup.
func TestExport_RefusesAnInstallThatIsNotStoppedAndDrained(t *testing.T) {
	cases := map[string]struct {
		state map[string][2]int // queue -> {ready, consumers}
		want  string
	}{
		"orchestrator still running": {map[string][2]int{"inbound.messages": {0, 1}}, "inbound.messages is being consumed"},
		"audit worker still running": {map[string][2]int{"audit.events": {0, 1}}, "audit.events is being consumed"},
		"messages not drained":       {map[string][2]int{"inbound.messages": {3, 0}}, "inbound.messages still holds 3 message(s)"},
		"audit events not drained":   {map[string][2]int{"audit.events": {7, 0}}, "audit.events still holds 7 message(s)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, _, _ := newExporter(t)
			e.Probe = queues(tc.state)

			_, _, err := e.Run(context.Background())

			require.ErrorContains(t, err, "not stopped and drained")
			require.ErrorContains(t, err, tc.want)
			assert.Empty(t, filesIn(t, e.BackupDir), "a refused export must write nothing")
		})
	}
}

func TestExport_UnreachableBrokerIsRefusedUnlessSkipped(t *testing.T) {
	e, _, _ := newExporter(t)
	e.Probe = func(context.Context, string) (int, int, error) { return 0, 0, errors.New("connection refused") }

	_, _, err := e.Run(context.Background())
	require.ErrorContains(t, err, "cannot confirm the install is stopped")
	require.ErrorContains(t, err, "--skip-stop-check")
	assert.Empty(t, filesIn(t, e.BackupDir))

	// Disaster recovery: the broker is gone, the operator says so explicitly.
	e.SkipStopCheck = true
	out := &bytes.Buffer{}
	e.Out = out
	m, _, err := e.Run(context.Background())
	require.NoError(t, err)
	assert.False(t, m.Source.Drained, "a set taken without the check must not claim the source was drained")
	assert.Contains(t, out.String(), "Stop check skipped")
}

// Safe by default: a set that leaves the machine is encrypted unless the operator opts out.
func TestExport_RefusesToWriteAnUnencryptedSetByDefault(t *testing.T) {
	e, _, _ := newExporter(t)
	e.Backup.Recipients = nil

	_, _, err := e.Run(context.Background())
	require.ErrorContains(t, err, "BACKUP_AGE_RECIPIENTS")
	assert.Empty(t, filesIn(t, e.BackupDir))

	e.AllowUnencrypted = true
	out := &bytes.Buffer{}
	e.Out = out
	m, _, err := e.Run(context.Background())
	require.NoError(t, err)
	assert.False(t, m.Encrypted)
	assert.Contains(t, out.String(), "NOT encrypted")
}

func TestExport_SignInSyncFailureStopsBeforeTheBackup(t *testing.T) {
	e, _, _ := newExporter(t)
	e.SyncSignIns = func(context.Context) (db.SignInInfo, error) {
		return db.SignInInfo{}, errors.New("cache unreachable: connection refused")
	}

	_, _, err := e.Run(context.Background())
	require.ErrorContains(t, err, "Google sign-ins")
	assert.Empty(t, filesIn(t, e.BackupDir))
}

func TestExport_ReportsSignInsThatCouldNotBeCarried(t *testing.T) {
	e, _, out := newExporter(t)
	e.SyncSignIns = func(context.Context) (db.SignInInfo, error) {
		return db.SignInInfo{InDatabase: 1, Unreadable: 2}, nil
	}
	_, _, err := e.Run(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out.String(), "2 cached Google sign-in(s) could not be read")
}

// --- import: the rules that apply before the database is touched ------------------------------

// importerThatMustNotTouchTheDatabase has no database at all: reaching it would panic.
func importerThatMustNotTouchTheDatabase(t *testing.T) Importer {
	t.Helper()
	return Importer{
		DatabaseURL: testDatabaseURL,
		BackupDir:   t.TempDir(),
		ArchiveDir:  t.TempDir(),
		Restore: db.RestoreOptions{Run: func(_ context.Context, name string, args []string, _ []string, stdin io.Reader, _ io.Writer) error {
			// Reading a dump to its end (`pg_restore -f -`) is allowed; restoring into a database is not.
			for _, a := range args {
				if a == "-d" {
					t.Fatal("pg_restore must not run against the database")
				}
			}
			_, err := io.Copy(io.Discard, stdin)
			return err
		}},
		Queues: []string{"inbound.messages", "audit.events"},
		Probe:  queues(nil),
		Out:    io.Discard,
	}
}

func TestImport_RejectsWhatIsNotABackup(t *testing.T) {
	i := importerThatMustNotTouchTheDatabase(t)
	dir := t.TempDir()
	stray := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(stray, []byte("x"), 0o600))

	_, err := i.Run(context.Background(), stray)
	require.ErrorContains(t, err, "neither a backup manifest")

	_, err = i.Run(context.Background(), filepath.Join(dir, "backup_2026-11-19_02-00-00.dump.age"))
	require.ErrorContains(t, err, "not found")
}

func TestImport_VerifiesTheSetBeforeAnythingElse(t *testing.T) {
	e, id, _ := newExporter(t)
	m, manifestPath, err := e.Run(context.Background())
	require.NoError(t, err)
	dumpPath := filepath.Join(e.BackupDir, m.Database.Name)

	i := importerThatMustNotTouchTheDatabase(t)
	i.Restore.Identities = []age.Identity{id}

	// Damage the dump: both ways of naming the set must refuse it.
	whole, err := os.ReadFile(dumpPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dumpPath, whole[:len(whole)-5], 0o600))
	for _, path := range []string{manifestPath, dumpPath} {
		_, err := i.Run(context.Background(), path)
		require.ErrorContains(t, err, "does not match its manifest", "given %s", filepath.Base(path))
	}
}

func TestImport_EncryptedBackupNeedsTheBackupKey(t *testing.T) {
	e, _, _ := newExporter(t)
	_, manifestPath, err := e.Run(context.Background())
	require.NoError(t, err)

	i := importerThatMustNotTouchTheDatabase(t) // no identities configured
	_, err = i.Run(context.Background(), manifestPath)
	require.ErrorContains(t, err, "BACKUP_AGE_IDENTITY_FILE")
}

// V4-43: only one install is ever live. An import into a running install is refused.
func TestImport_RefusesARunningTarget(t *testing.T) {
	e, id, _ := newExporter(t)
	_, manifestPath, err := e.Run(context.Background())
	require.NoError(t, err)

	i := importerThatMustNotTouchTheDatabase(t)
	i.Restore.Identities = []age.Identity{id}
	i.Probe = queues(map[string][2]int{"inbound.messages": {0, 1}})

	_, err = i.Run(context.Background(), manifestPath)
	require.ErrorContains(t, err, "this install is running")

	i.Probe = func(context.Context, string) (int, int, error) { return 0, 0, errors.New("connection refused") }
	_, err = i.Run(context.Background(), manifestPath)
	require.ErrorContains(t, err, "cannot confirm this install is stopped")
}

func TestNotCarried_NamesWhatTheRoadmapRequires(t *testing.T) {
	all := strings.ToLower(strings.Join(NotCarried, "\n"))
	for _, must := range []string{"chat window", "confirm", "24-hour", "dead-letter"} {
		assert.Contains(t, all, must)
	}
}

// Messages that failed for good stay in the old broker. An export says so, with the count, and
// records it; it does not refuse, because they are not work in flight.
func TestExport_ReportsDeadLetteredMessagesThatStayBehind(t *testing.T) {
	e, _, out := newExporter(t)
	e.Probe = queues(map[string][2]int{"inbound.messages.dlq": {2, 0}})

	m, _, err := e.Run(context.Background())
	require.NoError(t, err)

	assert.Contains(t, out.String(), "2 message(s) that failed for good sit in inbound.messages.dlq")
	assert.Contains(t, out.String(), "NOT in this backup")
	assert.Equal(t, db.QueueInfo{Ready: 2}, m.Source.Queues["inbound.messages.dlq"])
	assert.True(t, m.Source.Drained, "dead-lettered messages are not work in flight")
}

// A backup that cannot be opened must be found out before anything on the target is examined or
// changed: the import later empties the database. Wrong key, and a file cut short.
func TestImport_ABackupThatDoesNotOpenIsRefusedBeforeAnythingElse(t *testing.T) {
	e, id, _ := newExporter(t)
	m, manifestPath, err := e.Run(context.Background())
	require.NoError(t, err)

	t.Run("wrong backup key", func(t *testing.T) {
		other, err := age.GenerateX25519Identity()
		require.NoError(t, err)
		i := importerThatMustNotTouchTheDatabase(t)
		i.Restore.Identities = []age.Identity{other}
		i.Probe = func(context.Context, string) (int, int, error) {
			t.Fatal("the target must not even be probed when the backup does not open")
			return 0, 0, nil
		}

		_, err = i.Run(context.Background(), manifestPath)
		require.ErrorContains(t, err, "nothing was changed")
		require.ErrorContains(t, err, "backup key does not open")
	})

	t.Run("encrypted dump without a manifest, cut short", func(t *testing.T) {
		dir := t.TempDir()
		whole, err := os.ReadFile(filepath.Join(e.BackupDir, m.Database.Name))
		require.NoError(t, err)
		bare := filepath.Join(dir, "backup_2026-11-18_21-52-46.dump.age")
		require.NoError(t, os.WriteFile(bare, whole[:len(whole)-7], 0o600))
		i := importerThatMustNotTouchTheDatabase(t)
		i.Restore.Identities = []age.Identity{id}

		_, err = i.Run(context.Background(), bare)
		require.ErrorContains(t, err, "nothing was changed")
	})

	t.Run("archive bundle written for another key", func(t *testing.T) {
		other, err := age.GenerateX25519Identity()
		require.NoError(t, err)
		_, err = db.CheckArchives(filepath.Join(e.BackupDir, m.Archives.Name), []age.Identity{other})
		require.ErrorContains(t, err, "decrypt")
		files, err := db.CheckArchives(filepath.Join(e.BackupDir, m.Archives.Name), []age.Identity{id})
		require.NoError(t, err)
		assert.Equal(t, 1, files)
	})
}
