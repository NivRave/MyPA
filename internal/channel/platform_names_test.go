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

// scanned is one tree of production code the guard reads, and which of its files count.
type scanned struct {
	root     string // relative to the module root
	isSource func(rel string) bool
	minFiles int // the walk must see at least this many, or it is not seeing the tree
}

var productionCode = []scanned{
	{root: ".", minFiles: 100, isSource: func(rel string) bool {
		return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
	}},
	{root: "dashboard/src", minFiles: 1, isSource: func(rel string) bool {
		if strings.Contains(rel, "/__tests__/") || strings.Contains(rel, ".test.") {
			return false
		}
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs"} {
			if strings.HasSuffix(rel, ext) {
				return true
			}
		}
		return false
	}},
	{root: "config", minFiles: 1, isSource: func(string) bool { return true }},
}

// TestNoPlatformNamedOutsideAdapters is the guard of V4-31's "done when": no code outside the
// adapters names a platform. It reads production Go, the dashboard sources and the settings the
// image ships, comments included, so a platform switch cannot hide anywhere. Tests may name
// platforms (they build fixtures for them); .env.example and the docs document the platforms'
// own setting names and are not code.
func TestNoPlatformNamedOutsideAdapters(t *testing.T) {
	root := moduleRoot(t)
	for _, dir := range adapterDirs {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil || !info.IsDir() {
			t.Fatalf("adapter directory %s is missing: the allowlist is stale", dir)
		}
	}

	var offenders []string
	for _, tree := range productionCode {
		files := 0
		base := filepath.Join(root, filepath.FromSlash(tree.root))
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", ".next", "vendor":
					return filepath.SkipDir
				}
				if tree.root == "." && path == filepath.Join(root, "dashboard") {
					return filepath.SkipDir // its own tree below
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !tree.isSource(rel) {
				return nil
			}
			files++
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
			t.Fatalf("walking %s: %v", tree.root, err)
		}
		if files < tree.minFiles {
			t.Fatalf("scanned only %d source files under %s: the walk is not seeing the tree", files, tree.root)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("%d line(s) outside the adapters name a platform; move the code into the adapter or make it neutral:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// TestPlatformNamePatternCatchesEveryForm keeps the guard's pattern honest.
func TestPlatformNamePatternCatchesEveryForm(t *testing.T) {
	for _, s := range []string{"tgClient TelegramClient", `msg.Source == "whatsapp"`, "// a Twilio callback", "TWILIO_AUTH_TOKEN", "markdown.ToWhatsApp", "Instant Push (Telegram/WA)"} {
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
