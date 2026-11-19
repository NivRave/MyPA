package orchestrator

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// BL-21: the chat restore is retired. /restore only names the newest backup and replies with the
// steps for `transfer import --replace`; CONFIRM RESTORE is an ordinary confirmation again.

const (
	newestStamp = "backup_2026-11-14_03-00-00"
	dumpContent = "nightly-dump-bytes"
)

type restoreHarness struct {
	engine      *Engine
	store       *state.Store
	dir         string
	role        string
	mu          sync.Mutex
	replies     []string
	backupCalls int
}

func newRestoreHarness(t *testing.T) *restoreHarness {
	t.Helper()
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	t.Cleanup(cleanup)

	require.NotNil(t, engine.store, "the test engine uses a Redis-backed store")
	h := &restoreHarness{engine: engine, store: engine.store, dir: t.TempDir(), role: "admin"}
	engine.backups = backupOps{
		dir: h.dir,
		backup: func(string, string) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.backupCalls++
			return "", errors.New("no backup may be taken by /restore")
		},
	}
	t.Setenv("DATABASE_URL", "postgres://test")

	dbMock.EXPECT().GetUser("admin-1").DoAndReturn(func(string) (*models.User, error) {
		return &models.User{PlatformID: "admin-1", Role: h.role, FamilyGroup: "fam"}, nil
	}).AnyTimes()

	record := func(_ context.Context, _ string, text string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.replies = append(h.replies, text)
		return nil
	}
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(record).AnyTimes()
	engine.twClient.(*mocks.MockTwilioClient).EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(record).AnyTimes()
	return h
}

// writeSet puts a backup set (encrypted dump, archives and manifest) in the backup directory.
func (h *restoreHarness) writeSet(t *testing.T, stamp string) {
	t.Helper()
	for _, name := range []string{stamp + ".dump.age", stamp + ".archives.tar.age", stamp + ".manifest.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(h.dir, name), []byte(dumpContent), 0o600))
	}
}

func (h *restoreHarness) send(t *testing.T, source, text string) {
	t.Helper()
	require.NoError(t, h.engine.ProcessMessage(context.Background(), models.Message{
		UserID: "admin-1", ChatID: "chat-admin", Text: text, Source: source,
	}))
}

func (h *restoreHarness) all() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.replies, "\n---\n")
}

func (h *restoreHarness) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.replies = nil
}

// assertNothingRestored checks that no backup was taken and nothing is waiting for a confirmation.
func (h *restoreHarness) assertNothingRestored(t *testing.T) {
	t.Helper()
	assert.Zero(t, h.backupCalls, "a safety backup means a restore was under way")
	pending, err := h.store.GetAndClearPendingMetaTool(context.Background(), "admin-1")
	require.NoError(t, err)
	assert.Nil(t, pending, "/restore must not stage anything for CONFIRM")
}

// assertNoPrivateDetail checks that a reply shows neither where backups live nor what they contain.
func (h *restoreHarness) assertNoPrivateDetail(t *testing.T, reply string) {
	t.Helper()
	assert.NotContains(t, reply, h.dir, "the backup directory path must not be shown")
	assert.NotContains(t, reply, filepath.ToSlash(h.dir))
	assert.NotContains(t, reply, dumpContent, "no file content may be shown")
	assert.NotContains(t, reply, "postgres://", "no connection string may be shown")
	// The production backup directory is /backups. The only place it may appear is the fixed key
	// location of the documented command (RESTORE_AND_MIGRATE.md, section C).
	rest := strings.ReplaceAll(reply, "/backups/backup-key.txt", "")
	rest = strings.ReplaceAll(rest, "`backups/backup-key.txt`", "")
	assert.NotContains(t, rest, "/backups", "no backup path other than the documented key location")
	assert.NotContains(t, rest, defaultBackupDir)
}

func TestRestore_NonAdminIsRefused(t *testing.T) {
	for _, source := range []string{"telegram", "whatsapp"} {
		t.Run(source, func(t *testing.T) {
			h := newRestoreHarness(t)
			h.writeSet(t, newestStamp)
			h.role = "family"

			h.send(t, source, "/restore")

			reply := h.all()
			assert.Contains(t, reply, "Permission denied")
			assert.NotContains(t, reply, newestStamp, "a non-admin must not learn which backups exist")
			assert.NotContains(t, reply, "transfer import")
			h.assertNothingRestored(t)
		})
	}
}

// An admin gets the newest set named and the steps for the restore tool, identically on both
// channels, and nothing is restored, backed up or staged.
func TestRestore_AdminGetsInstructionsAndNothingIsRestored(t *testing.T) {
	for _, source := range []string{"telegram", "whatsapp"} {
		t.Run(source, func(t *testing.T) {
			h := newRestoreHarness(t)
			h.writeSet(t, "backup_2026-11-13_03-00-00")
			h.writeSet(t, newestStamp)

			h.send(t, source, "/restore")

			reply := h.all()
			require.Len(t, h.replies, 1, "one reply: %s", reply)
			assert.Contains(t, reply, newestStamp+".manifest.json", "names the newest set by its manifest")
			assert.NotContains(t, reply, "2026-11-13", "only the newest backup is named")
			assert.Contains(t, reply, "transfer import "+newestStamp+".manifest.json --replace")
			assert.Contains(t, reply, "BACKUP_AGE_IDENTITY_FILE", "an encrypted backup needs the private key")
			assert.Contains(t, reply, "docs/RESTORE_AND_MIGRATE.md")
			// V4-01: the app is one container; the tunnel goes first so nothing new comes in.
			assert.Contains(t, reply, "docker compose stop ngrok")
			assert.Contains(t, reply, "docker compose stop app audit-worker dashboard")
			assert.NotContains(t, reply, "CONFIRM RESTORE", "there is nothing to confirm any more")
			h.assertNoPrivateDetail(t, reply)
			h.assertNothingRestored(t)
		})
	}
}

// A dump from before backup sets existed has no manifest: the dump itself is named, and an
// unencrypted one does not ask for the private key.
func TestRestore_BareDumpIsNamedWhenThereIsNoManifest(t *testing.T) {
	h := newRestoreHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, newestStamp+".dump"), []byte(dumpContent), 0o600))

	h.send(t, "telegram", "/restore")

	reply := h.all()
	assert.Contains(t, reply, "transfer import "+newestStamp+".dump --replace")
	assert.NotContains(t, reply, "BACKUP_AGE_IDENTITY_FILE")
	h.assertNoPrivateDetail(t, reply)
	h.assertNothingRestored(t)
}

func TestRestore_NoBackupPresent_SaysSo(t *testing.T) {
	for _, source := range []string{"telegram", "whatsapp"} {
		t.Run(source, func(t *testing.T) {
			h := newRestoreHarness(t)
			require.NoError(t, os.WriteFile(filepath.Join(h.dir, "notes.txt"), []byte(dumpContent), 0o600))

			h.send(t, source, "/restore")

			reply := h.all()
			assert.Contains(t, reply, "No backup found")
			assert.NotContains(t, reply, "transfer import", "no command to run without a backup")
			h.assertNoPrivateDetail(t, reply)
			h.assertNothingRestored(t)
		})
	}
}

// An unreadable backup directory is reported as such, not as "no backup", and without the
// error text (which holds the path).
func TestRestore_UnreadableBackupDir_SaysSoWithoutThePath(t *testing.T) {
	h := newRestoreHarness(t)
	h.engine.backups.dir = filepath.Join(h.dir, "missing")

	h.send(t, "telegram", "/restore")

	reply := h.all()
	assert.Contains(t, reply, "could not be read")
	assert.NotContains(t, reply, "No backup found")
	assert.NotContains(t, reply, "missing")
	h.assertNoPrivateDetail(t, reply)
	h.assertNothingRestored(t)
}

// CONFIRM RESTORE after /restore is an ordinary confirmation with nothing pending.
func TestRestore_ConfirmRestoreRestoresNothing(t *testing.T) {
	for _, text := range []string{"CONFIRM RESTORE", "confirm restore", "CONFIRM"} {
		t.Run(text, func(t *testing.T) {
			h := newRestoreHarness(t)
			h.writeSet(t, newestStamp)

			h.send(t, "telegram", "/restore")
			h.reset()
			h.send(t, "telegram", text)

			reply := h.all()
			assert.Contains(t, reply, "No pending confirmation found")
			assert.NotContains(t, reply, "Confirmed and Executed")
			assert.NotContains(t, reply, newestStamp)
			h.assertNothingRestored(t)
		})
	}
}

// A restore staged by the retired command before the upgrade can still sit in Redis for up to its
// TTL. Confirming it must not run anything: the record is dropped and treated as absent.
func TestRestore_LegacyStagedRestoreCannotFire(t *testing.T) {
	for _, text := range []string{"CONFIRM RESTORE", "CONFIRM"} {
		t.Run(text, func(t *testing.T) {
			h := newRestoreHarness(t)
			h.writeSet(t, newestStamp)
			require.NoError(t, h.store.StagePendingMetaTool(context.Background(), "admin-1", state.PendingMetaToolAction{
				ToolName:  "restore",
				Args:      map[string]interface{}{"backup_path": filepath.Join(h.dir, newestStamp+".dump.age")},
				UserID:    "admin-1",
				CreatedAt: time.Now(),
			}, 5*time.Minute))

			h.send(t, "telegram", text)

			reply := h.all()
			assert.Contains(t, reply, "No pending confirmation found")
			assert.NotContains(t, reply, "Confirmed and Executed")
			assert.NotContains(t, reply, "mismatch")
			h.assertNoPrivateDetail(t, reply)
			h.assertNothingRestored(t) // also proves the record was removed, not re-staged
		})
	}
}

// The orchestrator no longer holds any way to restore a database: no non-test file in the
// package calls the restore functions of internal/db or internal/transfer.
func TestOrchestrator_HasNoRestorePath(t *testing.T) {
	forbidden := map[string]map[string]bool{
		"db":       {"RestoreDatabase": true, "RestoreOptionsFromEnv": true, "ResetPublicSchema": true, "ExtractArchives": true},
		"transfer": {"Importer": true},
	}
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && forbidden[pkg.Name][sel.Sel.Name] {
				t.Errorf("%s uses %s.%s: the chat restore is retired (BL-21)", fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
			}
			return true
		})
	}
}
