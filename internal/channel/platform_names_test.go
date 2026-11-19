package channel

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// platformName matches the name of a platform MyPA has an adapter for, in any case and inside any
// identifier, string or comment.
var platformName = regexp.MustCompile(`(?i)telegram|whatsapp|twilio`)

// adapterDirs is where platforms may be named: the adapters themselves and the one list that
// builds them. Paths are relative to the module root, with forward slashes.
var adapterDirs = []string{
	"internal/telegram/",
	"internal/twilio/",
	"internal/channel/adapters/",
}

// TestNoPlatformNamedOutsideAdapters is the guard of V4-31's "done when": no production Go code
// outside the adapters names a platform. Tests may name platforms (they build fixtures for them);
// production code may not, not even in a comment, so a platform switch cannot hide anywhere.
func TestNoPlatformNamedOutsideAdapters(t *testing.T) {
	root := moduleRoot(t)
	for _, dir := range adapterDirs {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil || !info.IsDir() {
			t.Fatalf("adapter directory %s is missing: the allowlist is stale", dir)
		}
	}

	scanned := 0
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".next", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		scanned++
		for _, dir := range adapterDirs {
			if strings.HasPrefix(rel, dir) {
				return nil
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if platformName.MatchString(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d production Go files from %s: the walk is not seeing the module", scanned, root)
	}
	if len(offenders) > 0 {
		t.Errorf("%d line(s) outside the adapters name a platform; move the code into the adapter or make it neutral:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// TestPlatformNamePatternCatchesEveryForm keeps the guard's pattern honest.
func TestPlatformNamePatternCatchesEveryForm(t *testing.T) {
	for _, s := range []string{"tgClient TelegramClient", `msg.Source == "whatsapp"`, "// a Twilio callback", "TWILIO_AUTH_TOKEN", "markdown.ToWhatsApp"} {
		if !platformName.MatchString(s) {
			t.Errorf("pattern misses %q", s)
		}
	}
	for _, s := range []string{"channel.Inbound", "adapter.Name()", "telemetry"} {
		if platformName.MatchString(s) {
			t.Errorf("pattern wrongly matches %q", s)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
