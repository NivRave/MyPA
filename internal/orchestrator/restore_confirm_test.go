package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type fakeBackups struct {
	mu          sync.Mutex
	dir         string
	backupErr   error
	backups     []string // paths created by backup()
	restored    []string // paths passed to restore()
	callOrder   []string
	backupCalls int
}

func (f *fakeBackups) ops() backupOps {
	return backupOps{
		dir: f.dir,
		backup: func(_ string, dir string, _ int) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.backupCalls++
			f.callOrder = append(f.callOrder, "backup")
			if f.backupErr != nil {
				return "", f.backupErr
			}
			// Name sorts after any pre-existing backup, so it becomes the "latest" on disk.
			p := filepath.Join(dir, "backup_2099-12-31_23-59-59.dump")
			if err := os.WriteFile(p, []byte("safety"), 0o600); err != nil {
				return "", err
			}
			f.backups = append(f.backups, p)
			return p, nil
		},
		restore: func(_ string, path string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.callOrder = append(f.callOrder, "restore")
			f.restored = append(f.restored, path)
			return nil
		},
	}
}

type restoreHarness struct {
	engine *Engine
	tg     *mocks.MockTelegramClient
	mr     *miniredis.Miniredis
	fake   *fakeBackups
	staged string // the backup file that exists before /restore
	role   string
}

func newRestoreHarness(t *testing.T) *restoreHarness {
	t.Helper()
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	t.Cleanup(cleanup)

	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	engine.store = store

	dir := t.TempDir()
	staged := filepath.Join(dir, "backup_2026-11-14_03-00-00.dump")
	require.NoError(t, os.WriteFile(staged, []byte("nightly"), 0o600))

	h := &restoreHarness{engine: engine, tg: tgMock, mr: mr, fake: &fakeBackups{dir: dir}, staged: staged, role: "admin"}
	engine.backups = h.fake.ops()
	t.Setenv("DATABASE_URL", "postgres://test")

	dbMock.EXPECT().GetUser("admin-1").DoAndReturn(func(string) (*models.User, error) {
		return &models.User{PlatformID: "admin-1", Role: h.role, FamilyGroup: "fam"}, nil
	}).AnyTimes()
	return h
}

// replies captures every message sent to the user.
func (h *restoreHarness) replies() *[]string {
	var mu sync.Mutex
	out := &[]string{}
	h.tg.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, text string) error {
		mu.Lock()
		defer mu.Unlock()
		*out = append(*out, text)
		return nil
	}).AnyTimes()
	return out
}

func (h *restoreHarness) send(t *testing.T, text string) {
	t.Helper()
	require.NoError(t, h.engine.ProcessMessage(context.Background(), models.Message{
		UserID: "admin-1", ChatID: "chat-admin", Text: text, Source: "telegram",
	}))
}

func joined(r *[]string) string { return strings.Join(*r, "\n---\n") }

// Acceptance: /restore alone never restores; it stages and asks for CONFIRM RESTORE.
func TestRestore_RequiresConfirmation(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()

	h.send(t, "/restore")

	assert.Empty(t, h.fake.restored, "/restore alone must never call RestoreDatabase")
	assert.Zero(t, h.fake.backupCalls, "no safety backup before confirmation")
	all := joined(out)
	assert.Contains(t, all, filepath.Base(h.staged), "prompt must name the backup file")
	assert.Contains(t, all, "CONFIRM RESTORE")
}

// Acceptance: CONFIRM RESTORE within TTL restores — the file shown at staging time, after a
// safety backup (which must not be mistaken for the "latest" backup to restore).
func TestRestore_ConfirmWithinTTL_BacksUpThenRestoresStagedFile(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()

	h.send(t, "/restore")
	h.send(t, "CONFIRM RESTORE")

	require.Equal(t, []string{h.staged}, h.fake.restored, "must restore the staged file, not the safety backup")
	assert.Equal(t, []string{"backup", "restore"}, h.fake.callOrder, "safety backup must precede restore")
	assert.Contains(t, joined(out), filepath.Base(h.fake.backups[0]), "user must be told where the safety backup is")
}

// Acceptance: expired confirmation → no-op message, no restore.
func TestRestore_ExpiredConfirmation_NoOp(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()

	h.send(t, "/restore")
	h.mr.FastForward(6 * time.Minute)
	h.send(t, "CONFIRM RESTORE")

	assert.Empty(t, h.fake.restored)
	assert.Contains(t, joined(out), "expired")
}

// A bare CONFIRM (meant for some other action) must not trigger a destructive restore.
func TestRestore_BareConfirmDoesNotRestore(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()

	h.send(t, "/restore")
	h.send(t, "CONFIRM")
	assert.Empty(t, h.fake.restored)
	assert.Contains(t, joined(out), "CONFIRM RESTORE")

	// The staged action survives the mismatch and can still be confirmed explicitly.
	h.send(t, "CONFIRM RESTORE")
	assert.Equal(t, []string{h.staged}, h.fake.restored)
}

func TestRestore_SafetyBackupFails_AbortsRestore(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()
	h.fake.backupErr = errors.New("pg_dump: disk full")

	h.send(t, "/restore")
	h.send(t, "CONFIRM RESTORE")

	assert.Empty(t, h.fake.restored, "no restore without a safety backup")
	assert.Contains(t, joined(out), "aborted")
}

// Authorization is re-checked at confirmation time.
func TestRestore_RoleRevokedBeforeConfirm_Denied(t *testing.T) {
	h := newRestoreHarness(t)
	out := h.replies()

	h.send(t, "/restore")
	h.role = "family"
	h.send(t, "CONFIRM RESTORE")

	assert.Empty(t, h.fake.restored)
	assert.Contains(t, joined(out), "Permission denied")
}
