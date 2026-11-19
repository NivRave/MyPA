package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatModelName matches a Gemini chat model name such as gemini-2.5-flash. Embedding models
// (gemini-embedding-001) do not match: they are a separate setting (V4-03).
var chatModelName = regexp.MustCompile(`gemini-[0-9][A-Za-z0-9.\-]*`)

// decidesTheDefault reports whether a tracked file is one that can decide which model a running
// install uses: production Go code, configuration and deployment files. Tests, documentation and
// tooling notes are excluded; they may mention any model.
func decidesTheDefault(name string) bool {
	switch {
	case strings.HasPrefix(name, "docs/"), strings.HasPrefix(name, ".agents/"), strings.HasPrefix(name, ".claude/"),
		strings.HasPrefix(name, ".github/"), strings.HasPrefix(name, "dashboard/node_modules/"):
		return false
	case strings.HasSuffix(name, "_test.go"), strings.HasSuffix(name, ".md"):
		return false
	case strings.HasSuffix(name, ".go"), strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"),
		strings.HasSuffix(name, ".example"), strings.HasSuffix(name, ".json"),
		strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".tsx"),
		strings.HasPrefix(filepath.Base(name), "Dockerfile"):
		return true
	}
	return false
}

func trackedFiles(t *testing.T) (root string, names []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err)
	return root, strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
}

// V4-09 "done when": one current default is defined in one place. Among the files that can
// decide which chat model an install runs, a model name appears exactly once: the constant.
func TestDefaultChatModelIsDefinedInOnePlace(t *testing.T) {
	root, names := trackedFiles(t)

	var found []string
	for _, name := range names {
		if !decidesTheDefault(name) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			continue // deleted in the working tree, or binary
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, model := range chatModelName.FindAllString(line, -1) {
				found = append(found, fmt.Sprintf("%s:%d %s", name, i+1, model))
			}
		}
	}

	require.Len(t, found, 1, "a chat model must be named in exactly one place (DefaultGeminiModel in internal/config/config.go); found:\n  %s", strings.Join(found, "\n  "))
	assert.True(t, strings.HasPrefix(found[0], "internal/config/config.go:"), "the one definition must be DefaultGeminiModel, found %s", found[0])
	assert.True(t, strings.HasSuffix(found[0], " "+DefaultGeminiModel), "the one definition must be DefaultGeminiModel, found %s", found[0])
	assert.Regexp(t, "^"+chatModelName.String()+"$", DefaultGeminiModel)
}

// An install that sets no model gets the default: neither the settings file shipped in the image
// nor the settings template names another one.
func TestDefaultChatModel_IsWhatAnInstallGetsWithoutSettingOne(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	shipped, err := os.ReadFile(filepath.Join(root, "config", "config.yaml"))
	require.NoError(t, err, "the settings file copied into the image must exist")

	// A scratch directory laid out like the image: ./config/config.yaml and nothing else. No .env
	// is in reach, so a developer's own settings cannot leak into the test (or into this process).
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config", "config.yaml"), shipped, 0o644))
	t.Chdir(dir)
	t.Setenv("GEMINI_MODEL", "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultGeminiModel, cfg.Gemini.Model, "the shipped settings file must not name a different model")

	// The shipped file really was read: a value it does set is in effect.
	require.Contains(t, string(shipped), "gateway_port", "expected the shipped settings file to set the gateway port")
	assert.NotZero(t, cfg.Server.GatewayPort)
}

// An explicit setting still wins over the default.
func TestDefaultChatModel_IsOverriddenByTheSetting(t *testing.T) {
	t.Setenv("GEMINI_MODEL", "some-other-model")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "some-other-model", cfg.Gemini.Model)
}

// The settings template must not pin a model (that would be a second place), and the reference
// documentation must show the real default.
func TestDefaultChatModel_TemplateAndDocsFollowTheConstant(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	template, err := os.ReadFile(filepath.Join(root, ".env.example"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(template), "\n") {
		assert.False(t, strings.HasPrefix(strings.TrimSpace(line), "GEMINI_MODEL="), ".env.example must leave GEMINI_MODEL unset so the built-in default applies: %q", line)
	}
	assert.Contains(t, string(template), "GEMINI_MODEL", "the template must still show that the setting exists")

	system, err := os.ReadFile(filepath.Join(root, "SYSTEM.md"))
	require.NoError(t, err)
	var row string
	for _, line := range strings.Split(string(system), "\n") {
		if strings.HasPrefix(line, "| `GEMINI_MODEL` |") {
			row = line
		}
	}
	require.NotEmpty(t, row, "SYSTEM.md must document GEMINI_MODEL")
	assert.Contains(t, row, "`\""+DefaultGeminiModel+"\"`", "SYSTEM.md must show the current default")
	for _, model := range chatModelName.FindAllString(row, -1) {
		assert.Equal(t, DefaultGeminiModel, model, "SYSTEM.md names a different default model")
	}
}
