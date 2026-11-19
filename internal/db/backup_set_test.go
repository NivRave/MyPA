package db

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSetFixture returns a backup dir, an archive dir with two archive files (plus files that
// must not be carried), and an age key pair.
func newSetFixture(t *testing.T) (backupDir, archiveDir string, id *age.X25519Identity) {
	t.Helper()
	backupDir, archiveDir = t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "audit_archive_1700000000.jsonl.gz"), []byte("first archive"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "audit_archive_1700086400.jsonl.gz"), []byte("second archive"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "audit_archive_1700172800.jsonl.gz.tmp"), []byte("half written"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(archiveDir, "subdir"), 0o755))
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return backupDir, archiveDir, id
}

func readTarNames(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[h.Name] = string(body)
	}
}

// V4-43: a backup is the database dump plus the archive files, described by a manifest.
func TestCreateBackupSet_DumpArchivesAndManifest(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, id := newSetFixture(t)
	opts := BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}}

	m, manifestPath, err := CreateBackupSet(testDBURL, backupDir, opts, SetOptions{
		ArchiveDir:    archiveDir,
		SchemaVersion: 2,
		Source:        &SourceInfo{Drained: true, Queues: map[string]QueueInfo{"inbound.messages": {}}},
		NotCarried:    []string{"the 24-hour chat window"},
	})
	require.NoError(t, err)

	assert.Equal(t, ManifestFormat, m.Format)
	assert.True(t, m.Encrypted)
	assert.Equal(t, int64(2), m.SchemaVersion)
	assert.Equal(t, 2, m.ArchiveFiles, "temporary files and directories are not archive files")
	require.NotNil(t, m.Archives)
	assert.True(t, strings.HasSuffix(m.Database.Name, ".dump.age"))
	assert.True(t, strings.HasSuffix(m.Archives.Name, ".archives.tar.age"))
	assert.Equal(t, ManifestPathFor(filepath.Join(backupDir, m.Database.Name)), manifestPath)
	assert.Equal(t, setBase(m.Database.Name), strings.TrimSuffix(filepath.Base(manifestPath), manifestExt), "the files of a set share one name")

	// The manifest on disk is what was returned, and the set verifies.
	onDisk, err := VerifyBackupSet(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, m, onDisk)
	assert.True(t, onDisk.Source.Drained)
	assert.Equal(t, []string{"the 24-hour chat window"}, onDisk.NotCarried)

	// The archive files are encrypted at rest and come back intact.
	raw, err := os.ReadFile(filepath.Join(backupDir, m.Archives.Name))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "first archive", "archive files must not be readable without the key")
	plain, err := age.Decrypt(bytes.NewReader(raw), id)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"audit_archive_1700000000.jsonl.gz": "first archive",
		"audit_archive_1700086400.jsonl.gz": "second archive",
	}, readTarNames(t, plain))

	// Nothing temporary is left behind.
	entries, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), e.Name())
	}
}

func TestCreateBackupSet_WithoutArchiveDirIsDatabaseOnly(t *testing.T) {
	useFakeRunner(t)
	backupDir := t.TempDir()

	for _, dir := range []string{"", filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir()} {
		m, manifestPath, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: RetentionPolicy{KeepLatest: 10}}, SetOptions{ArchiveDir: dir, SchemaVersion: -1})
		require.NoError(t, err)
		assert.Nil(t, m.Archives)
		assert.Zero(t, m.ArchiveFiles)
		assert.False(t, m.Encrypted)
		_, err = VerifyBackupSet(manifestPath)
		require.NoError(t, err)
		time.Sleep(1100 * time.Millisecond) // backup names have one-second resolution
	}
}

// A failure after the dump leaves no partial set that could later be mistaken for a backup.
func TestCreateBackupSet_FailureLeavesNothingBehind(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, _ := newSetFixture(t)
	real := bundleArchives
	bundleArchives = func(path, dir string, names []string, recipients []age.Recipient) error {
		// Write the bundle for real, then fail as a full disk would at the very end.
		if err := real(path, dir, names, recipients); err != nil {
			return err
		}
		return errors.New("no space left on device")
	}
	t.Cleanup(func() { bundleArchives = real })

	_, _, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: DefaultRetention}, SetOptions{ArchiveDir: archiveDir})
	require.ErrorContains(t, err, "no space left")

	entries, readErr := os.ReadDir(backupDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "a failed backup must leave neither a dump, an archive bundle nor a manifest")
}

func TestVerifyBackupSet_DetectsDamage(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, id := newSetFixture(t)
	m, manifestPath, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}}, SetOptions{ArchiveDir: archiveDir})
	require.NoError(t, err)
	dumpPath := filepath.Join(backupDir, m.Database.Name)
	archivePath := filepath.Join(backupDir, m.Archives.Name)
	original, err := os.ReadFile(dumpPath)
	require.NoError(t, err)

	t.Run("truncated dump", func(t *testing.T) {
		require.NoError(t, os.WriteFile(dumpPath, original[:len(original)/2], 0o600))
		_, err := VerifyBackupSet(manifestPath)
		require.ErrorContains(t, err, "does not match its manifest")
		require.NoError(t, os.WriteFile(dumpPath, original, 0o600))
	})
	t.Run("same size, different bytes", func(t *testing.T) {
		flipped := append([]byte(nil), original...)
		flipped[len(flipped)-1] ^= 0xff
		require.NoError(t, os.WriteFile(dumpPath, flipped, 0o600))
		_, err := VerifyBackupSet(manifestPath)
		require.ErrorContains(t, err, "does not match its manifest")
		require.NoError(t, os.WriteFile(dumpPath, original, 0o600))
	})
	t.Run("missing archive bundle", func(t *testing.T) {
		require.NoError(t, os.Rename(archivePath, archivePath+".moved"))
		_, err := VerifyBackupSet(manifestPath)
		require.ErrorContains(t, err, "incomplete")
		require.NoError(t, os.Rename(archivePath+".moved", archivePath))
	})
	t.Run("intact again", func(t *testing.T) {
		_, err := VerifyBackupSet(manifestPath)
		require.NoError(t, err)
	})
}

// A manifest is input from disk: it must not be able to point the restore at other files.
func TestReadManifest_RejectsHostileManifests(t *testing.T) {
	dir := t.TempDir()
	write := func(m map[string]any) string {
		body, err := json.Marshal(m)
		require.NoError(t, err)
		p := filepath.Join(dir, "backup_2026-11-19_02-00-00.manifest.json")
		require.NoError(t, os.WriteFile(p, body, 0o600))
		return p
	}
	good := map[string]any{"file": "backup_2026-11-19_02-00-00.dump.age", "bytes": 1, "sha256": "00"}

	cases := map[string]map[string]any{
		"unknown format":           {"format": 99, "database": good},
		"database outside the dir": {"format": 1, "database": map[string]any{"file": "../backup_x.dump", "bytes": 1, "sha256": "00"}},
		"database with a path":     {"format": 1, "database": map[string]any{"file": "backup_/etc/passwd.dump", "bytes": 1, "sha256": "00"}},
		"database not a backup":    {"format": 1, "database": map[string]any{"file": "id_rsa", "bytes": 1, "sha256": "00"}},
		"archives outside the dir": {"format": 1, "database": good, "archives": map[string]any{"file": "backup_../../x.archives.tar", "bytes": 1, "sha256": "00"}},
		"archives not an archive":  {"format": 1, "database": good, "archives": map[string]any{"file": "backup_2026.dump.age", "bytes": 1, "sha256": "00"}},
		"windows path in archives": {"format": 1, "database": good, "archives": map[string]any{"file": `backup_..\x.archives.tar`, "bytes": 1, "sha256": "00"}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadManifest(write(m))
			require.Error(t, err)
		})
	}

	t.Run("not json", func(t *testing.T) {
		p := filepath.Join(dir, "broken.manifest.json")
		require.NoError(t, os.WriteFile(p, []byte("{"), 0o600))
		_, err := ReadManifest(p)
		require.Error(t, err)
	})
	t.Run("oversized", func(t *testing.T) {
		p := filepath.Join(dir, "huge.manifest.json")
		require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte(" "), manifestSize+10), 0o600))
		_, err := ReadManifest(p)
		require.ErrorContains(t, err, "too large")
	})
}

func TestExtractArchives_RoundTrip(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, id := newSetFixture(t)
	m, _, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}}, SetOptions{ArchiveDir: archiveDir})
	require.NoError(t, err)
	archivePath := filepath.Join(backupDir, m.Archives.Name)
	dest := filepath.Join(t.TempDir(), "archives") // does not exist yet

	restored, already, err := ExtractArchives(archivePath, dest, []age.Identity{id})
	require.NoError(t, err)
	assert.Equal(t, 2, restored)
	assert.Zero(t, already)
	body, err := os.ReadFile(filepath.Join(dest, "audit_archive_1700086400.jsonl.gz"))
	require.NoError(t, err)
	assert.Equal(t, "second archive", string(body))

	// Running it again changes nothing.
	restored, already, err = ExtractArchives(archivePath, dest, []age.Identity{id})
	require.NoError(t, err)
	assert.Zero(t, restored)
	assert.Equal(t, 2, already)

	// An existing archive with other content is never overwritten.
	clash := filepath.Join(dest, "audit_archive_1700000000.jsonl.gz")
	require.NoError(t, os.WriteFile(clash, []byte("something else"), 0o644))
	_, _, err = ExtractArchives(archivePath, dest, []age.Identity{id})
	require.ErrorContains(t, err, "not overwritten")
	body, err = os.ReadFile(clash)
	require.NoError(t, err)
	assert.Equal(t, "something else", string(body))
	leftovers, err := filepath.Glob(filepath.Join(dest, "*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestExtractArchives_WrongOrMissingKey(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, id := newSetFixture(t)
	m, _, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}}, SetOptions{ArchiveDir: archiveDir})
	require.NoError(t, err)
	archivePath := filepath.Join(backupDir, m.Archives.Name)
	other, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	dest := t.TempDir()

	_, _, err = ExtractArchives(archivePath, dest, nil)
	require.ErrorContains(t, err, "BACKUP_AGE_IDENTITY_FILE")
	_, _, err = ExtractArchives(archivePath, dest, []age.Identity{other})
	require.ErrorContains(t, err, "decrypt")
	entries, err := os.ReadDir(dest)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing may be written when the key is wrong")
}

// A bundle is input: an entry must never be written outside the archive directory, and only
// plain files are accepted.
func TestExtractArchives_RejectsHostileBundles(t *testing.T) {
	build := func(headers ...*tar.Header) string {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range headers {
			body := []byte("payload")
			if h.Typeflag == tar.TypeReg && h.Size == 0 {
				h.Size = int64(len(body))
			}
			require.NoError(t, tw.WriteHeader(h))
			if h.Typeflag == tar.TypeReg {
				_, err := tw.Write(body[:h.Size])
				require.NoError(t, err)
			}
		}
		require.NoError(t, tw.Close())
		p := filepath.Join(t.TempDir(), "backup_2026-11-19_02-00-00.archives.tar")
		require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o600))
		return p
	}
	cases := map[string]*tar.Header{
		"parent directory":   {Name: "../escape.jsonl.gz", Typeflag: tar.TypeReg, Mode: 0o644},
		"absolute path":      {Name: "/etc/cron.d/evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"nested path":        {Name: "sub/file.jsonl.gz", Typeflag: tar.TypeReg, Mode: 0o644},
		"windows separators": {Name: `..\escape.jsonl.gz`, Typeflag: tar.TypeReg, Mode: 0o644},
		"hidden file":        {Name: ".profile", Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink":            {Name: "link.jsonl.gz", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"directory":          {Name: "dir", Typeflag: tar.TypeDir, Mode: 0o755},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			outer := t.TempDir()
			dest := filepath.Join(outer, "archives")
			_, _, err := ExtractArchives(build(&tar.Header{Name: "ok.jsonl.gz", Typeflag: tar.TypeReg, Mode: 0o644}, header), dest, nil)
			require.ErrorContains(t, err, "not a plain archive file")

			// Only the one legitimate file exists, and nothing appeared outside dest.
			inside, err := os.ReadDir(dest)
			require.NoError(t, err)
			require.Len(t, inside, 1)
			assert.Equal(t, "ok.jsonl.gz", inside[0].Name())
			outside, err := os.ReadDir(outer)
			require.NoError(t, err)
			assert.Len(t, outside, 1, "something was written outside the archive directory")
		})
	}

	t.Run("truncated bundle", func(t *testing.T) {
		p := build(&tar.Header{Name: "ok.jsonl.gz", Typeflag: tar.TypeReg, Mode: 0o644})
		whole, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, whole[:515], 0o600)) // the 512-byte header plus 3 of the 7 body bytes
		dest := t.TempDir()
		_, _, err = ExtractArchives(p, dest, nil)
		require.Error(t, err)
		left, readErr := os.ReadDir(dest)
		require.NoError(t, readErr)
		assert.Empty(t, left, "a half-restored archive file must not be left behind")
	})
}

// Retention removes a pruned backup's whole set and leaves kept sets complete.
func TestApplyRetention_RemovesWholeSets(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	touch := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	set := func(ts time.Time) string {
		base := backupPrefix + ts.Format(backupTimeLayout)
		touch(base + ".dump.age")
		touch(base + ".archives.tar.age")
		touch(base + ".manifest.json")
		return base
	}
	fresh := set(now.Add(-2 * time.Hour))
	old := set(now.Add(-100 * 24 * time.Hour))
	touch("notes.txt")

	removed, err := ApplyRetention(dir, RetentionPolicy{KeepLatest: 1, Daily: 7, Weekly: 4}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{old + ".dump.age"}, removed)

	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{fresh + ".dump.age", fresh + ".archives.tar.age", fresh + ".manifest.json", "notes.txt"}, names)
}

// A fresh-schema restore must not drop objects one by one, must be all-or-nothing, and must not
// depend on the database role the dump was taken under.
func TestRestoreDatabase_FreshSchemaArguments(t *testing.T) {
	runner := useFakeRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "backup_2026-11-19_02-00-00.dump")
	require.NoError(t, os.WriteFile(path, []byte("PGDMP"), 0o600))

	require.NoError(t, RestoreDatabase(testDBURL, path, RestoreOptions{FreshSchema: true}))

	require.Len(t, runner.calls, 1)
	args := strings.Join(runner.calls[0], " ")
	assert.Contains(t, args, "--single-transaction")
	assert.Contains(t, args, "--no-owner")
	assert.Contains(t, args, "--no-privileges")
	assert.NotContains(t, args, " -c ")
	assert.NotContains(t, args, "s3cr3t-pw", "the password stays out of argv")
}

// The options' runner is used instead of the package default, for dump and for restore.
func TestBackupAndRestore_UseTheSuppliedRunner(t *testing.T) {
	var ran []string
	run := func(_ context.Context, name string, _ []string, _ []string, stdin io.Reader, stdout io.Writer) error {
		ran = append(ran, name)
		if name == "pg_dump" {
			_, err := stdout.Write([]byte("PGDMP"))
			return err
		}
		_, err := io.Copy(io.Discard, stdin)
		return err
	}
	dir := t.TempDir()
	path, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention, Run: run})
	require.NoError(t, err)
	require.NoError(t, RestoreDatabase(testDBURL, path, RestoreOptions{Run: run}))
	assert.Equal(t, []string{"pg_dump", "pg_restore"}, ran)
}

// Backup names have one-second resolution. A second backup taken within the same second (a
// safety backup right after a set was copied in, or /backup sent twice) must get its own name:
// an existing backup is never overwritten.
func TestBackup_NeverOverwritesAnExistingBackup(t *testing.T) {
	runner := useFakeRunner(t)
	dir := t.TempDir()
	opts := BackupOptions{Retention: RetentionPolicy{KeepLatest: 10}}

	runner.dumpData = []byte("first backup")
	first, err := BackupDatabase(testDBURL, dir, opts)
	require.NoError(t, err)
	runner.dumpData = []byte("second backup")
	second, err := BackupDatabase(testDBURL, dir, opts)
	require.NoError(t, err)
	runner.dumpData = []byte("third backup, as a set")
	m, _, err := CreateBackupSet(testDBURL, dir, opts, SetOptions{})
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.NotEqual(t, filepath.Base(second), m.Database.Name)
	body, err := os.ReadFile(first)
	require.NoError(t, err)
	assert.Equal(t, "first backup", string(body), "the first backup must be intact")
	body, err = os.ReadFile(second)
	require.NoError(t, err)
	assert.Equal(t, "second backup", string(body))

	// A set's name is also taken by its manifest and archive bundle, not only by its dump.
	stamp := time.Now().Add(time.Hour).Truncate(time.Second)
	for i := 0; i < 3; i++ { // cover the second in which the next backup starts
		name := backupPrefix + stamp.Add(time.Duration(i)*time.Second).Format(backupTimeLayout) + manifestExt
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600))
	}
	taken := backupPrefix + stamp.Format(backupTimeLayout)
	assert.True(t, backupNameTaken(dir, taken), "a name used by any file of a set is taken")
	assert.False(t, backupNameTaken(dir, backupPrefix+stamp.Add(-time.Minute).Format(backupTimeLayout)))
}

// The safety backup taken before a restore must not prune: retention could remove the backup
// that is about to be restored.
func TestBackup_SkipRetentionKeepsEveryExistingBackup(t *testing.T) {
	useFakeRunner(t)
	dir := t.TempDir()
	old := backupPrefix + time.Now().Add(-400*24*time.Hour).Format(backupTimeLayout) + ".dump.age"
	require.NoError(t, os.WriteFile(filepath.Join(dir, old), []byte("the backup to restore"), 0o600))
	prune := RetentionPolicy{KeepLatest: 1, Daily: 1, Weekly: 1}

	_, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: prune, SkipRetention: true})
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(dir, old))
	require.NoError(t, statErr, "the old backup must survive a backup taken with SkipRetention")

	time.Sleep(1100 * time.Millisecond)
	_, _, err = CreateBackupSet(testDBURL, dir, BackupOptions{Retention: prune, SkipRetention: true}, SetOptions{})
	require.NoError(t, err)
	_, statErr = os.Stat(filepath.Join(dir, old))
	require.NoError(t, statErr)

	// The same backup without the flag does prune it: the flag is what protected it.
	time.Sleep(1100 * time.Millisecond)
	_, err = BackupDatabase(testDBURL, dir, BackupOptions{Retention: prune})
	require.NoError(t, err)
	_, statErr = os.Stat(filepath.Join(dir, old))
	assert.True(t, os.IsNotExist(statErr), "without SkipRetention the policy applies")
}

// CheckDump reads a backup to its end without a database. It is what lets a restore refuse a
// backup it cannot open before the target is emptied.
func TestCheckDump(t *testing.T) {
	runner := useFakeRunner(t)
	dir := t.TempDir()
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	other, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	encrypted, err := BackupDatabase(testDBURL, dir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}})
	require.NoError(t, err)

	t.Run("the right key reads it to the end, without a database", func(t *testing.T) {
		runner.calls = nil
		require.NoError(t, CheckDump(encrypted, RestoreOptions{Identities: []age.Identity{id}}))
		require.Len(t, runner.calls, 1)
		assert.Equal(t, []string{"pg_restore", "-f", "-"}, runner.calls[0], "the check must not name a database")
		assert.Equal(t, runner.dumpData, runner.restored, "the whole decrypted dump is read")
	})
	t.Run("wrong key", func(t *testing.T) {
		runner.calls = nil
		err := CheckDump(encrypted, RestoreOptions{Identities: []age.Identity{other}})
		require.ErrorContains(t, err, "backup key does not open")
		assert.Empty(t, runner.calls)
	})
	t.Run("no key", func(t *testing.T) {
		require.ErrorContains(t, CheckDump(encrypted, RestoreOptions{}), "BACKUP_AGE_IDENTITY_FILE")
	})
	t.Run("encrypted file cut short", func(t *testing.T) {
		whole, err := os.ReadFile(encrypted)
		require.NoError(t, err)
		short := filepath.Join(t.TempDir(), filepath.Base(encrypted))
		require.NoError(t, os.WriteFile(short, whole[:len(whole)-4], 0o600))
		require.Error(t, CheckDump(short, RestoreOptions{Identities: []age.Identity{id}}))
	})
	t.Run("archive that pg_restore cannot read", func(t *testing.T) {
		broken := func(context.Context, string, []string, []string, io.Reader, io.Writer) error {
			return errors.New("pg_restore: error: could not read from input file: end of file")
		}
		err := CheckDump(encrypted, RestoreOptions{Identities: []age.Identity{id}, Run: broken})
		require.ErrorContains(t, err, "cannot be read to its end")
	})
	t.Run("missing file", func(t *testing.T) {
		require.ErrorContains(t, CheckDump(filepath.Join(dir, "backup_1999-01-01_00-00-00.dump"), RestoreOptions{}), "does not exist")
	})
}

// CheckArchives validates a bundle exactly as an extraction would, and writes nothing.
func TestCheckArchives_ValidatesWithoutWriting(t *testing.T) {
	useFakeRunner(t)
	backupDir, archiveDir, id := newSetFixture(t)
	m, _, err := CreateBackupSet(testDBURL, backupDir, BackupOptions{Retention: DefaultRetention, Recipients: []age.Recipient{id.Recipient()}}, SetOptions{ArchiveDir: archiveDir})
	require.NoError(t, err)
	archivePath := filepath.Join(backupDir, m.Archives.Name)
	before, err := os.ReadDir(backupDir)
	require.NoError(t, err)

	files, err := CheckArchives(archivePath, []age.Identity{id})
	require.NoError(t, err)
	assert.Equal(t, 2, files)
	after, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "a check writes nothing")

	other, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	_, err = CheckArchives(archivePath, []age.Identity{other})
	require.ErrorContains(t, err, "decrypt")

	// A hostile entry is rejected by the check too, before any extraction is attempted.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "../escape.jsonl.gz", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}))
	_, err = tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	hostile := filepath.Join(t.TempDir(), "backup_2026-11-19_02-00-00.archives.tar")
	require.NoError(t, os.WriteFile(hostile, buf.Bytes(), 0o600))
	_, err = CheckArchives(hostile, nil)
	require.ErrorContains(t, err, "not a plain archive file")
}
