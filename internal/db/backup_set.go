package db

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

// A backup set (V4-43) is everything a restore needs, as files that share one timestamp:
//
//	backup_<ts>.dump[.age]          the database (pg_dump custom format), as before
//	backup_<ts>.archives.tar[.age]  the archive files (audit archives today)
//	backup_<ts>.manifest.json       what the set contains: sizes, SHA-256, schema version
//
// The dump is unchanged from earlier releases, so a bare dump taken before sets existed (the
// pre-V4 baseline) restores through the same path, only without archive files. The manifest is
// written last: a dump without a manifest is either such an older backup or an interrupted run.
//
// The manifest is plain JSON and not secret. Its checksums guard against truncated, corrupted or
// mismatched files; they are not a signature. Anyone who can write to the backup directory can
// replace a whole set, exactly as they could replace a bare dump before.

const (
	// ManifestFormat is the version of the manifest layout.
	ManifestFormat = 1

	archivesExt  = ".archives.tar"
	manifestExt  = ".manifest.json"
	ageExt       = ".age"
	maxArchived  = 4 << 30 // largest single archive file accepted on restore (4 GiB)
	manifestSize = 1 << 20 // largest manifest accepted (1 MiB)
)

// SetFile describes one file of a backup set.
type SetFile struct {
	Name   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// QueueInfo is the state of one work queue when the backup was taken.
type QueueInfo struct {
	Ready     int `json:"ready"`
	Consumers int `json:"consumers"`
}

// SignInInfo says how many Google sign-ins the backup carries.
type SignInInfo struct {
	// InDatabase is the number of encrypted sign-in copies in the dump.
	InDatabase int `json:"in_database"`
	// CopiedNow is how many of them were copied from the cache just before the dump.
	CopiedNow int `json:"copied_now"`
	// Unreadable counts cached sign-ins that could not be read and therefore have no copy.
	Unreadable int `json:"unreadable"`
}

// SourceInfo records the state of the install the backup was taken from.
type SourceInfo struct {
	// Stopped is true when the queues were empty and nothing was consuming them: the app was
	// stopped and no message was in flight.
	Stopped       bool                 `json:"stopped"`
	Queues        map[string]QueueInfo `json:"queues,omitempty"`
	GoogleSignIns *SignInInfo          `json:"google_sign_ins,omitempty"`
}

// Manifest describes a backup set.
type Manifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Encrypted bool      `json:"encrypted"`
	// SchemaVersion is the migration version of the dumped database; -1 when it was not read.
	SchemaVersion int64   `json:"schema_version"`
	Database      SetFile `json:"database"`
	// Archives is nil when the set carries no archive files.
	Archives     *SetFile    `json:"archives,omitempty"`
	ArchiveFiles int         `json:"archive_files"`
	Source       *SourceInfo `json:"source,omitempty"`
	// NotCarried lists the state a restore will not bring back.
	NotCarried []string `json:"not_carried,omitempty"`
}

// SetOptions is what CreateBackupSet adds to a plain database backup.
type SetOptions struct {
	// ArchiveDir holds the archive files to include. Empty or missing: the set has none.
	ArchiveDir string
	// SchemaVersion of the database being dumped; -1 when unknown.
	SchemaVersion int64
	Source        *SourceInfo
	NotCarried    []string
}

// setBase returns the name shared by the files of the set a dump belongs to.
func setBase(dumpName string) string {
	return strings.TrimSuffix(strings.TrimSuffix(dumpName, ageExt), plainExt)
}

// setCompanions returns the names the other files of a dump's set may have.
func setCompanions(dumpName string) []string {
	base := setBase(dumpName)
	return []string{base + archivesExt, base + archivesExt + ageExt, base + manifestExt}
}

// ManifestPathFor returns where the manifest of a dump's set lives (whether or not it exists).
func ManifestPathFor(dumpPath string) string {
	return filepath.Join(filepath.Dir(dumpPath), setBase(filepath.Base(dumpPath))+manifestExt)
}

// IsManifestFile reports whether name is the manifest of a backup set.
func IsManifestFile(name string) bool {
	return strings.HasPrefix(name, backupPrefix) && strings.HasSuffix(name, manifestExt)
}

// CreateBackupSet takes a database backup, adds the archive files and writes the manifest. It
// returns the manifest and its path. Retention is applied afterwards, to whole sets.
func CreateBackupSet(databaseURL, backupDir string, opts BackupOptions, set SetOptions) (Manifest, string, error) {
	dumpPath, err := dumpDatabase(databaseURL, backupDir, opts)
	if err != nil {
		return Manifest{}, "", err
	}
	base := setBase(filepath.Base(dumpPath))
	// Anything that fails from here on leaves no half-written set behind.
	created := []string{dumpPath}
	fail := func(err error) (Manifest, string, error) {
		for _, p := range created {
			_ = os.Remove(p)
		}
		return Manifest{}, "", err
	}

	m := Manifest{
		Format:        ManifestFormat,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		Encrypted:     len(opts.Recipients) > 0,
		SchemaVersion: set.SchemaVersion,
		Source:        set.Source,
		NotCarried:    set.NotCarried,
	}
	if m.Database, err = describeFile(dumpPath); err != nil {
		return fail(err)
	}

	if files, err := archiveFiles(set.ArchiveDir); err != nil {
		return fail(err)
	} else if len(files) > 0 {
		archivePath := filepath.Join(backupDir, base+archivesExt)
		if m.Encrypted {
			archivePath += ageExt
		}
		created = append(created, archivePath)
		if err := bundleArchives(archivePath, set.ArchiveDir, files, opts.Recipients); err != nil {
			return fail(err)
		}
		described, err := describeFile(archivePath)
		if err != nil {
			return fail(err)
		}
		m.Archives, m.ArchiveFiles = &described, len(files)
	}

	manifestPath := filepath.Join(backupDir, base+manifestExt)
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := writeFileAtomically(manifestPath, append(body, '\n'), 0o600); err != nil {
		return fail(fmt.Errorf("failed to write backup manifest: %w", err))
	}
	slog.Info("Backup set created", "manifest", manifestPath, "archive_files", m.ArchiveFiles, "encrypted", m.Encrypted)

	if !opts.SkipRetention {
		pruneBackups(backupDir, opts.Retention)
	}
	return m, manifestPath, nil
}

// archiveFileName is what an archive file may be called. Archive files are written by MyPA with
// generated names; anything else in the directory is not carried, and nothing else is accepted
// back on restore.
var archiveFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)

// archiveFiles lists the regular files of dir that belong in a backup, sorted by name.
func archiveFiles(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read archive directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() || strings.HasSuffix(e.Name(), ".tmp") || !archiveFileName.MatchString(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// bundleArchives is writeArchives; a variable so a test can make the step fail.
var bundleArchives = writeArchives

// writeArchives streams the named files of dir into a tar at path, age-encrypted when there are
// recipients. It writes under a temporary name and renames.
func writeArchives(path, dir string, names []string, recipients []age.Recipient) (err error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create archive bundle: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	var sink io.Writer = f
	var enc io.WriteCloser
	if len(recipients) > 0 {
		if enc, err = age.Encrypt(f, recipients...); err != nil {
			return fmt.Errorf("failed to start encryption: %w", err)
		}
		sink = enc
	}
	tw := tar.NewWriter(sink)
	for _, name := range names {
		if err = addToTar(tw, filepath.Join(dir, name), name); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return fmt.Errorf("failed to finish archive bundle: %w", err)
	}
	if enc != nil {
		if err = enc.Close(); err != nil {
			return fmt.Errorf("failed to finish encryption: %w", err)
		}
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("failed to sync archive bundle: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("failed to close archive bundle: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to finalize archive bundle: %w", err)
	}
	return nil
}

func addToTar(tw *tar.Writer, path, name string) error {
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open archive file %s: %w", name, err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat archive file %s: %w", name, err)
	}
	header := &tar.Header{Name: name, Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to add %s to the archive bundle: %w", name, err)
	}
	// Exactly Size bytes: a file that grows while it is read must not corrupt the tar.
	if _, err := io.CopyN(tw, src, info.Size()); err != nil {
		return fmt.Errorf("failed to add %s to the archive bundle: %w", name, err)
	}
	return nil
}

// describeFile returns the name, size and SHA-256 of a file.
func describeFile(path string) (SetFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return SetFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return SetFile{}, fmt.Errorf("failed to read %s: %w", filepath.Base(path), err)
	}
	return SetFile{Name: filepath.Base(path), Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadManifest parses a manifest file. It rejects a manifest it cannot fully trust to describe
// files in its own directory: unknown formats, and file names that are not plain backup names.
func ReadManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("cannot open the backup manifest: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, manifestSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("cannot read the backup manifest: %w", err)
	}
	if len(body) > manifestSize {
		return Manifest{}, errors.New("the backup manifest is too large to be genuine")
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("the backup manifest is not valid JSON: %w", err)
	}
	if m.Format != ManifestFormat {
		return Manifest{}, fmt.Errorf("backup manifest format %d is not supported by this release (it reads format %d)", m.Format, ManifestFormat)
	}
	if !isBackupFile(m.Database.Name) {
		return Manifest{}, fmt.Errorf("the manifest names a database file that is not a backup: %q", m.Database.Name)
	}
	if m.Archives != nil {
		n := m.Archives.Name
		ok := strings.HasPrefix(n, backupPrefix) && (strings.HasSuffix(n, archivesExt) || strings.HasSuffix(n, archivesExt+ageExt))
		if !ok || n != filepath.Base(n) || strings.ContainsAny(n, `/\`) {
			return Manifest{}, fmt.Errorf("the manifest names an archive file that is not part of a backup: %q", n)
		}
	}
	if n := m.Database.Name; n != filepath.Base(n) || strings.ContainsAny(n, `/\`) {
		return Manifest{}, fmt.Errorf("the manifest names a database file outside its directory: %q", n)
	}
	return m, nil
}

// VerifyBackupSet reads the manifest at manifestPath and checks that every file it names is
// present next to it with the recorded size and SHA-256.
func VerifyBackupSet(manifestPath string) (Manifest, error) {
	m, err := ReadManifest(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	dir := filepath.Dir(manifestPath)
	files := []SetFile{m.Database}
	if m.Archives != nil {
		files = append(files, *m.Archives)
	}
	for _, want := range files {
		got, err := describeFile(filepath.Join(dir, want.Name))
		if err != nil {
			return Manifest{}, fmt.Errorf("backup set is incomplete: %s is missing or unreadable", want.Name)
		}
		if got.Bytes != want.Bytes || !strings.EqualFold(got.SHA256, want.SHA256) {
			return Manifest{}, fmt.Errorf("backup set is damaged: %s does not match its manifest (size or checksum differs)", want.Name)
		}
	}
	return m, nil
}

// ExtractArchives restores the archive files of a set into destDir. A file that already exists
// with the same content is left alone; one that exists with different content stops the restore,
// because an archive is never overwritten. Entry names are validated: nothing can be written
// outside destDir, and only regular files are accepted.
func ExtractArchives(archivePath, destDir string, identities []age.Identity) (restored, alreadyThere int, err error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot open the archive bundle: %w", err)
	}
	defer f.Close()

	var in io.Reader = bufio.NewReader(f)
	if strings.HasSuffix(archivePath, ageExt) {
		if len(identities) == 0 {
			return 0, 0, errors.New("the archive bundle is encrypted: set BACKUP_AGE_IDENTITY_FILE to the age private key to restore")
		}
		if in, err = age.Decrypt(in, identities...); err != nil {
			return 0, 0, fmt.Errorf("failed to decrypt the archive bundle: %w", err)
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, 0, fmt.Errorf("failed to create the archive directory: %w", err)
	}

	tr := tar.NewReader(in)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return restored, alreadyThere, nil
		}
		if err != nil {
			return restored, alreadyThere, fmt.Errorf("the archive bundle is damaged: %w", err)
		}
		name := header.Name
		if header.Typeflag != tar.TypeReg || !archiveFileName.MatchString(name) || name != filepath.Base(name) {
			return restored, alreadyThere, fmt.Errorf("the archive bundle contains an entry that is not a plain archive file: %q", name)
		}
		if header.Size < 0 || header.Size > maxArchived {
			return restored, alreadyThere, fmt.Errorf("the archive bundle contains an implausibly large file: %q", name)
		}
		wrote, err := extractOne(tr, header.Size, filepath.Join(destDir, name))
		if err != nil {
			return restored, alreadyThere, err
		}
		if wrote {
			restored++
		} else {
			alreadyThere++
		}
	}
}

// extractOne writes one archive file. It reports false when an identical file was already there.
func extractOne(src io.Reader, size int64, dest string) (bool, error) {
	name := filepath.Base(dest)
	tmp := dest + ".restore.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("failed to write archive file %s: %w", name, err)
	}
	h := sha256.New()
	_, err = io.CopyN(io.MultiWriter(out, h), src, size)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("failed to write archive file %s: %w", name, err)
	}

	if existing, statErr := describeFile(dest); statErr == nil {
		_ = os.Remove(tmp)
		if existing.SHA256 == hex.EncodeToString(h.Sum(nil)) {
			return false, nil
		}
		return false, fmt.Errorf("archive file %s already exists with different content; it was not overwritten", name)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("failed to finalize archive file %s: %w", name, err)
	}
	return true, nil
}

// ResetPublicSchema drops everything in the database's public schema and recreates it empty.
// A restore into a fresh schema (RestoreOptions.FreshSchema) starts from here: restoring over
// existing objects would keep whatever the dump does not know about, such as tables added by a
// later migration, and the migrations that follow the restore would then collide with them.
func ResetPublicSchema(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS public CASCADE`); err != nil {
		return fmt.Errorf("drop the public schema: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `CREATE SCHEMA public`); err != nil {
		return fmt.Errorf("recreate the public schema: %w", err)
	}
	return nil
}

// HasUserData reports whether the database holds a paired user. An install without one has never
// been used, so a restore can replace its (empty) contents without asking.
func HasUserData(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	exists, err := tableExists(ctx, sqlDB, "users")
	if err != nil || !exists {
		return false, err
	}
	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return n > 0, nil
}
