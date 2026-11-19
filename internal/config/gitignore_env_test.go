package config

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-3: every .env variant (backups, per-environment copies) holds real secrets and must never be
// committable; only the template .env.example is tracked. Asks git itself (--no-index evaluates the
// patterns regardless of what is tracked), so the test checks behaviour, not .gitignore's text.
func TestGitignore_IgnoresEveryEnvCopyButTheExample(t *testing.T) {
	// BL-27: without git this guard checks nothing, and a skip reads as a pass. It fails instead.
	_, err := exec.LookPath("git")
	require.NoError(t, err, "git must be on PATH: this guard fails, never skips, when it cannot run")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	ignored := func(path string) bool {
		cmd := exec.Command("git", "check-ignore", "-q", "--no-index", path)
		cmd.Dir = root
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false // not ignored
		}
		require.NoError(t, err, "git check-ignore %s", path)
		return true
	}

	for _, p := range []string{".env", ".env.bak", ".env.local", ".env.prod", ".env.backup-20261116", "dashboard/.env.local"} {
		assert.True(t, ignored(p), "%s must be ignored", p)
	}
	assert.False(t, ignored(".env.example"), ".env.example is the committed template")
}
