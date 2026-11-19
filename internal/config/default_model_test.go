package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatModelName finds a Gemini chat model named in any spelling a person might write it in: the
// API name (gemini-2.5-flash) and the display name (Gemini 2.5 Flash), in any letter case. The
// name must be followed by a version digit, so the setting (GEMINI_MODEL), identifiers
// (DefaultGeminiModel) and embedding models (gemini-embedding-001, a separate setting, V4-03) do
// not match.
var chatModelName = regexp.MustCompile(`(?i)\bgemini[- ]?[0-9][0-9A-Za-z.\-]*`)

// apiModelName is the form the constant itself must have: what the API accepts.
var apiModelName = regexp.MustCompile(`^gemini-[0-9][0-9a-z.\-]*$`)

func TestChatModelNamePattern(t *testing.T) {
	for text, want := range map[string]string{
		`model = "gemini-2.5-flash"`:        "gemini-2.5-flash",
		`<option>Gemini 2.5 Flash</option>`: "Gemini 2.5",
		`GEMINI-3.6-FLASH`:                  "GEMINI-3.6-FLASH",
		`uses gemini2.5 by default`:         "gemini2.5",
	} {
		assert.Equal(t, want, chatModelName.FindString(text), text)
	}
	for _, text := range []string{
		"GEMINI_MODEL=", "gemini.model", "DefaultGeminiModel", "cfg.Gemini.Model", "GEMINI_API_KEY",
		"gemini-embedding-001", "Primary Gemini chat/tool-calling model", "Google Gemini", "NewGeminiClient(3)",
	} {
		assert.Empty(t, chatModelName.FindString(text), "%q is not a chat model name", text)
	}
}

// skippedDirs are never searched: version control, dependencies, build output, agent notes,
// local data and documentation. None of them can decide which model an install uses.
var skippedDirs = map[string]bool{
	".git": true, ".agents": true, ".claude": true, ".github": true, ".idea": true, ".vscode": true,
	"node_modules": true, ".next": true, "bin": true, "backups": true, "docs": true, "coverage": true,
}

// decidesTheDefault reports whether a file (path relative to the repository root, with forward
// slashes) is one that can decide which model a running install uses: production Go code,
// configuration and deployment files. Tests and Markdown are excluded; they may mention any model.
func decidesTheDefault(name string) bool {
	base := name[strings.LastIndex(name, "/")+1:]
	switch {
	case strings.HasSuffix(name, "_test.go"), strings.HasSuffix(name, ".md"),
		strings.HasSuffix(name, ".test.ts"), strings.HasSuffix(name, ".test.tsx"), strings.Contains(name, "/__tests__/"):
		return false
	case base == ".env.example", strings.HasPrefix(base, "Dockerfile"),
		strings.HasSuffix(name, ".go"), strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"),
		strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".tsx"):
		return true
	case strings.HasSuffix(name, ".json"):
		return base != "package-lock.json"
	}
	return false
}

// V4-09 "done when": one current default is defined in one place. Among the files that can
// decide which chat model an install runs, a model name appears exactly once: the constant.
//
// The test walks the source tree itself. It must not depend on git or on the environment: other
// tests in this package clear the environment, and a guard that skips looks exactly like a guard
// that passes.
func TestDefaultChatModelIsDefinedInOnePlace(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	var found []string
	searched := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !decidesTheDefault(name) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		searched++
		for i, line := range strings.Split(string(data), "\n") {
			for _, model := range chatModelName.FindAllString(line, -1) {
				found = append(found, fmt.Sprintf("%s:%d %s", name, i+1, model))
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, searched, 100, "the search must have covered the source tree, not an empty directory")

	require.Len(t, found, 1, "a chat model must be named in exactly one place (DefaultGeminiModel in internal/config/config.go); found:\n  %s", strings.Join(found, "\n  "))
	assert.True(t, strings.HasPrefix(found[0], "internal/config/config.go:"), "the one definition must be DefaultGeminiModel, found %s", found[0])
	assert.True(t, strings.HasSuffix(found[0], " "+DefaultGeminiModel), "the one definition must be DefaultGeminiModel, found %s", found[0])
	assert.Regexp(t, apiModelName, DefaultGeminiModel, "the default must be an API model name")
}

// The files the guard has to cover are covered, and the ones it must leave alone are left alone.
func TestDecidesTheDefault(t *testing.T) {
	for _, name := range []string{
		"internal/config/config.go", "internal/llm/client.go", "cmd/mypa/worker.go",
		"config/config.yaml", "docker-compose.yml", ".env.example", "Dockerfile", "dashboard/Dockerfile",
		"dashboard/src/app/page.tsx", "dashboard/src/app/settings/page.tsx", "dashboard/package.json",
	} {
		assert.True(t, decidesTheDefault(name), "%s can decide the default and must be searched", name)
	}
	for _, name := range []string{
		"internal/config/config_test.go", "internal/llm/llm_test.go", "README.md", "SYSTEM.md",
		".env", "dashboard/package-lock.json", "go.sum",
		"dashboard/src/__tests__/settings_page.test.tsx", "dashboard/src/lib/client.test.ts",
	} {
		assert.False(t, decidesTheDefault(name), "%s must not be searched", name)
	}
}

// An install that sets no model gets the default: the settings file shipped in the image does
// not name another one.
func TestDefaultChatModel_IsWhatAnInstallGetsWithoutSettingOne(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	shipped, err := os.ReadFile(filepath.Join(root, "config", "config.yaml"))
	require.NoError(t, err, "the settings file copied into the image must exist")

	// A scratch directory laid out like the image: ./config/config.yaml and nothing else. No .env
	// is in reach, so a developer's own settings cannot leak into the test (or into this process).
	// One value of the copy is changed to something no default has, to prove below that the file
	// is really what Load reads here; everything else, including what it says or does not say
	// about the model, is the shipped file.
	const portInFile, marker = "port: 8000", "port: 18080"
	require.Equal(t, 1, strings.Count(string(shipped), portInFile), "expected the shipped settings file to set the app port once")
	copyOfShipped := strings.Replace(string(shipped), portInFile, marker, 1)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config", "config.yaml"), []byte(copyOfShipped), 0o644))
	t.Chdir(dir)
	t.Setenv("GEMINI_MODEL", "")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 18080, cfg.Server.Port, "Load did not read ./config/config.yaml, so this test would prove nothing about it")
	assert.Equal(t, DefaultGeminiModel, cfg.Gemini.Model, "the shipped settings file must not name a different model")
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
