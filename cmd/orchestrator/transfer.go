package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/transfer"
	"gorm.io/gorm"
)

// The restore-and-migrate tool (V4-43): `orchestrator transfer <verb>`. It runs as a one-shot
// process next to a STOPPED app (the `transfer` compose service), never inside the running
// orchestrator. See docs/RESTORE_AND_MIGRATE.md for the procedure.

const transferUsage = `Usage: orchestrator transfer <command>

  export        Take the final backup set of this (stopped) install.
                  --allow-unencrypted   write the set without age encryption
                  --skip-stop-check     do not require empty, unconsumed queues
                  --skip-sign-in-check  do not copy cached Google sign-ins into the database first
  import FILE   Restore a backup set (backup_<time>.manifest.json) or a database dump
                (backup_<time>.dump[.age]) into this (stopped) install and migrate it.
                  --replace             allow replacing a database that already holds users
                  --no-migrate          leave the schema as backed up (to roll back to the
                                        release that took the backup)
                  --skip-stop-check     do not require unconsumed queues
  verify FILE   Check a backup set against its manifest without restoring anything.
  export-keys   Write the data encryption key (and optionally the backup key) to a
                passphrase-protected file. The passphrase is generated and shown once.
                  --out FILE            where to write it (default: <backup dir>/mypa-keys.age)
                  --with-backup-key     include the private backup key (BACKUP_AGE_IDENTITY_FILE)
  import-keys   Read such a file and write the settings lines to put into .env.
                  --in FILE             the key file (default: <backup dir>/mypa-keys.age)
                  --passphrase-file F   a file holding the passphrase
                  --out FILE            where to write the lines (default: <backup dir>/mypa-keys.env)

FILE may be a name inside the backup directory.
`

// googleProvider is the provider name of the durable Google sign-in copies (durability module).
const googleProvider = "google_oauth"

// transferQueues are the work queues whose state shows whether an install is stopped.
var transferQueues = []string{broker.InboundMessagesQueue, broker.AuditEventsQueue}

// transferDeps is everything the tool talks to. Production builds it from the environment;
// tests build it against throwaway containers.
type transferDeps struct {
	DatabaseURL string
	BackupDir   string
	ArchiveDir  string
	Backup      db.BackupOptions
	Restore     db.RestoreOptions
	Probe       transfer.QueueProbe
	// Store is the cache holding Google sign-ins. Nil: the cache is not available.
	Store *state.Store

	// The keys this install is configured with, for export-keys.
	EncryptionKey    string
	BackupRecipients string
	IdentityFile     string

	Out io.Writer
}

// transferMain is the entry point used by main().
func transferMain(args []string) int {
	out := os.Stdout
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, transferUsage)
		return 2
	}
	deps, cleanup, err := newTransferDeps(out)
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	return runTransfer(ctx, args, deps)
}

// newTransferDeps reads the same settings the app uses.
func newTransferDeps(out io.Writer) (transferDeps, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return transferDeps{}, nil, fmt.Errorf("cannot load the configuration: %w", err)
	}
	backup, err := db.BackupOptionsFromEnv()
	if err != nil {
		return transferDeps{}, nil, err
	}
	restore, err := db.RestoreOptionsFromEnv()
	if err != nil {
		return transferDeps{}, nil, err
	}
	deps := transferDeps{
		DatabaseURL:      cfg.Database.URL,
		BackupDir:        doctorBackupDir,
		ArchiveDir:       transfer.ArchiveDirFromEnv(),
		Backup:           backup,
		Restore:          restore,
		Probe:            queueProbe(cfg.RabbitMQ.URL),
		EncryptionKey:    strings.TrimSpace(os.Getenv("ENCRYPTION_KEY")),
		BackupRecipients: strings.TrimSpace(os.Getenv("BACKUP_AGE_RECIPIENTS")),
		IdentityFile:     strings.TrimSpace(os.Getenv("BACKUP_AGE_IDENTITY_FILE")),
		Out:              out,
	}
	cleanup := func() {}
	// The cache is optional here: export says clearly when it could not be reached.
	if store, err := state.NewStore(cfg.Redis.URL); err == nil {
		deps.Store = store
		cleanup = func() { _ = store.Close() }
	}
	return deps, cleanup, nil
}

// queueProbe looks at a queue through its own short-lived connection. A queue that does not
// exist yet (an install that was never started) has nothing waiting and nobody consuming.
func queueProbe(amqpURL string) transfer.QueueProbe {
	return func(ctx context.Context, queue string) (int, int, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		s, err := broker.InspectQueue(probeCtx, amqpURL, queue)
		if broker.IsQueueMissing(err) {
			return 0, 0, nil
		}
		return s.Ready, s.Consumers, err
	}
}

// runTransfer executes one command and returns the process exit code.
func runTransfer(ctx context.Context, args []string, deps transferDeps) int {
	out := deps.Out
	fail := func(err error) int {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	verb, rest := args[0], args[1:]
	flags := flag.NewFlagSet("transfer "+verb, flag.ContinueOnError)
	flags.SetOutput(out)

	switch verb {
	case "export":
		allowPlain := flags.Bool("allow-unencrypted", false, "")
		skipStop := flags.Bool("skip-stop-check", false, "")
		skipSignIns := flags.Bool("skip-sign-in-check", false, "")
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		return transferExport(ctx, deps, *allowPlain, *skipStop, *skipSignIns)

	case "import":
		replace := flags.Bool("replace", false, "")
		skipStop := flags.Bool("skip-stop-check", false, "")
		noMigrate := flags.Bool("no-migrate", false, "")
		file, err := parseWithFile(flags, rest)
		if err != nil {
			return fail(err)
		}
		return transferImport(ctx, deps, inBackupDir(deps.BackupDir, file), *replace, *skipStop, *noMigrate)

	case "verify":
		file, err := parseWithFile(flags, rest)
		if err != nil {
			return fail(err)
		}
		path := inBackupDir(deps.BackupDir, file)
		if db.IsBackupFile(filepath.Base(path)) {
			path = db.ManifestPathFor(path)
		}
		m, err := db.VerifyBackupSet(path)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "✓ %s is complete and matches its manifest (taken %s, schema version %d, %d archive file(s), encrypted: %v).\n",
			filepath.Base(path), m.CreatedAt.Format("2006-01-02 15:04 MST"), m.SchemaVersion, m.ArchiveFiles, m.Encrypted)
		return 0

	case "export-keys":
		outFile := flags.String("out", filepath.Join(deps.BackupDir, "mypa-keys.age"), "")
		withBackupKey := flags.Bool("with-backup-key", false, "")
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		if err := transferExportKeys(deps, *outFile, *withBackupKey); err != nil {
			return fail(err)
		}
		return 0

	case "import-keys":
		inFile := flags.String("in", filepath.Join(deps.BackupDir, "mypa-keys.age"), "")
		passFile := flags.String("passphrase-file", "", "")
		outFile := flags.String("out", filepath.Join(deps.BackupDir, "mypa-keys.env"), "")
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		if err := transferImportKeys(deps, *inFile, *passFile, *outFile); err != nil {
			return fail(err)
		}
		return 0

	default:
		fmt.Fprintf(out, "✗ unknown command %q\n\n%s", verb, transferUsage)
		return 2
	}
}

// parseWithFile parses flags that may come before or after the one FILE argument.
func parseWithFile(flags *flag.FlagSet, args []string) (string, error) {
	var file string
	var flagArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else if file == "" {
			file = a
		} else {
			return "", fmt.Errorf("expected one FILE, got %q and %q", file, a)
		}
	}
	if err := flags.Parse(flagArgs); err != nil {
		return "", err
	}
	if file == "" {
		return "", errors.New("name the backup to use: a backup_<time>.manifest.json or a backup_<time>.dump[.age]")
	}
	return file, nil
}

// inBackupDir resolves a bare file name against the backup directory.
func inBackupDir(backupDir, file string) string {
	if filepath.IsAbs(file) || strings.ContainsAny(file, `/\`) {
		return file
	}
	return filepath.Join(backupDir, file)
}

func openTransferDB(databaseURL string) (*db.Client, *sql.DB, error) {
	client, err := db.Open(databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot connect to the database: %w", err)
	}
	sqlDB, err := client.DB.DB()
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("cannot use the database connection: %w", err)
	}
	return client, sqlDB, nil
}

func transferExport(ctx context.Context, deps transferDeps, allowPlain, skipStop, skipSignIns bool) int {
	out := deps.Out
	client, sqlDB, err := openTransferDB(deps.DatabaseURL)
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	defer client.Close()

	exporter := transfer.Exporter{
		DatabaseURL:      deps.DatabaseURL,
		BackupDir:        deps.BackupDir,
		ArchiveDir:       deps.ArchiveDir,
		Backup:           deps.Backup,
		AllowUnencrypted: allowPlain,
		Queues:           transferQueues,
		Probe:            deps.Probe,
		SkipStopCheck:    skipStop,
		SchemaVersion: func(ctx context.Context) (int64, error) {
			current, _, err := db.SchemaVersion(ctx, sqlDB)
			return current, err
		},
		Out: out,
	}
	switch {
	case skipSignIns:
		fmt.Fprintln(out, "! Google sign-in check skipped: a sign-in that exists only in the cache is not in this backup.")
	case deps.Store == nil:
		fmt.Fprintln(out, "✗ The cache (Redis) cannot be reached, so cached Google sign-ins cannot be copied into the database. "+
			"Start Redis, or pass --skip-sign-in-check if the cache is lost: people whose sign-in was only there will need /connect again.")
		return 1
	default:
		exporter.SyncSignIns = signInSync(client, sqlDB, deps.Store).Run
	}

	if _, _, err := exporter.Run(ctx); err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	return 0
}

// signInSync binds transfer.SignInSync to the cache and the database. The copy is encrypted the
// same way the durability module encrypts it when someone finishes /connect.
func signInSync(client *db.Client, sqlDB *sql.DB, store *state.Store) transfer.SignInSync {
	return transfer.SignInSync{
		CachedUsers: store.OAuthTokenUserIDs,
		CachedToken: store.GetOAuthToken,
		HasCopy: func(_ context.Context, userID string) (bool, error) {
			cred, err := client.GetEncryptedCredential(userID, googleProvider)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return cred != nil && len(cred.Ciphertext) > 0, nil
		},
		SaveCopy: func(_ context.Context, userID string, token []byte) error {
			ciphertext, err := crypto.Encrypt(token, "")
			if err != nil {
				return err
			}
			return client.SaveEncryptedCredential(models.EncryptedCredential{UserID: userID, Provider: googleProvider, Ciphertext: ciphertext})
		},
		CountCopies: func(ctx context.Context) (int, error) {
			return db.CountCredentialCopies(ctx, sqlDB, googleProvider)
		},
	}
}

func transferImport(ctx context.Context, deps transferDeps, path string, replace, skipStop, noMigrate bool) int {
	out := deps.Out
	client, sqlDB, err := openTransferDB(deps.DatabaseURL)
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	defer client.Close()

	importer := transfer.Importer{
		DatabaseURL:   deps.DatabaseURL,
		ArchiveDir:    deps.ArchiveDir,
		BackupDir:     deps.BackupDir,
		Restore:       deps.Restore,
		Backup:        deps.Backup,
		Replace:       replace,
		SkipMigrate:   noMigrate,
		Queues:        transferQueues,
		Probe:         deps.Probe,
		SkipStopCheck: skipStop,
		DB:            sqlDB,
		Migrate:       db.Migrate,
		SchemaVersion: db.SchemaVersion,
		CheckDataKey:  checkDataKey,
		CountSignIns: func(ctx context.Context, sqlDB *sql.DB) (int, error) {
			return db.CountCredentialCopies(ctx, sqlDB, googleProvider)
		},
		Out: out,
	}
	res, err := importer.Run(ctx, path)
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	if len(res.Problems) > 0 {
		return 1
	}
	return 0
}

// checkDataKey tries this install's data encryption key on the secrets in the restored database.
func checkDataKey(ctx context.Context, sqlDB *sql.DB) (checked, failed int, err error) {
	ciphertexts, err := db.CredentialCiphertexts(ctx, sqlDB, 25)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range ciphertexts {
		checked++
		if _, err := crypto.Decrypt(c, ""); err != nil {
			failed++
		}
	}
	return checked, failed, nil
}

func transferExportKeys(deps transferDeps, outFile string, withBackupKey bool) error {
	out := deps.Out
	if deps.EncryptionKey == "" {
		return errors.New("ENCRYPTION_KEY is not set for this install, so there is no data encryption key to export")
	}
	keys := transfer.Keys{EncryptionKey: deps.EncryptionKey, BackupRecipients: deps.BackupRecipients}
	if withBackupKey {
		if deps.IdentityFile == "" {
			return errors.New("--with-backup-key needs BACKUP_AGE_IDENTITY_FILE to name the private backup key file")
		}
		identity, err := os.ReadFile(deps.IdentityFile)
		if err != nil {
			return fmt.Errorf("cannot read the backup key file: %w", err)
		}
		keys.BackupIdentity = string(identity)
	}
	passphrase, err := transfer.GeneratePassphrase()
	if err != nil {
		return err
	}
	// Never overwrite: an existing key file may be the only copy of an older export.
	f, err := os.OpenFile(outFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; move it away or choose another --out", outFile)
		}
		return fmt.Errorf("cannot create the key file: %w", err)
	}
	if err := transfer.ExportKeys(f, keys, passphrase); err != nil {
		_ = f.Close()
		_ = os.Remove(outFile)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(outFile)
		return fmt.Errorf("cannot finish the key file: %w", err)
	}

	fmt.Fprintf(out, "✓ Keys written to %s (data encryption key", outFile)
	if keys.BackupIdentity != "" {
		fmt.Fprint(out, ", private backup key")
	}
	fmt.Fprintln(out, ").")
	fmt.Fprintln(out, "  The file opens only with this passphrase. It is shown once and stored nowhere:")
	fmt.Fprintf(out, "\n      %s\n\n", passphrase)
	fmt.Fprintln(out, "  Carry the passphrase separately from the file (write it down; do not put both in one message or on one disk).")
	if keys.BackupIdentity == "" {
		fmt.Fprintln(out, "  The private backup key is not in this file. Bring it from where you keep it (your password manager): the import needs it to open the backup.")
	}
	fmt.Fprintln(out, "  On the new install: transfer import-keys --passphrase-file <file holding the passphrase>. Delete the key file once the new install works.")
	return nil
}

func transferImportKeys(deps transferDeps, inFile, passFile, outFile string) error {
	out := deps.Out
	if passFile == "" {
		return errors.New("name a file that holds the passphrase with --passphrase-file (it is not taken on the command line, where it would be visible to other processes)")
	}
	passphrase, err := os.ReadFile(passFile)
	if err != nil {
		return fmt.Errorf("cannot read the passphrase file: %w", err)
	}
	f, err := os.Open(inFile)
	if err != nil {
		return fmt.Errorf("cannot open the key file: %w", err)
	}
	defer f.Close()
	keys, err := transfer.ImportKeys(f, string(passphrase))
	if err != nil {
		return err
	}

	identityFile := ""
	if keys.BackupIdentity != "" {
		identityFile = filepath.Join(filepath.Dir(outFile), "mypa-backup-key.txt")
		if err := writeNewSecretFile(identityFile, []byte(keys.BackupIdentity)); err != nil {
			return err
		}
	}
	if err := writeNewSecretFile(outFile, keys.EnvLines(identityFile)); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ Settings lines written to %s.\n", outFile)
	if identityFile != "" {
		fmt.Fprintf(out, "✓ Private backup key written to %s.\n", identityFile)
	}
	fmt.Fprintln(out, "  Copy the lines into this install's .env, then delete these files and the passphrase file.")
	if deps.EncryptionKey != "" && deps.EncryptionKey != keys.EncryptionKey {
		fmt.Fprintln(out, "! This install currently has a different ENCRYPTION_KEY. Restored secrets open only with the imported one.")
	}
	return nil
}

// writeNewSecretFile creates a file only the owner can read and refuses to replace an existing one.
func writeNewSecretFile(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; move it away first", path)
		}
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return f.Close()
}
