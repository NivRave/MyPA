package pgtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-11: integration tests used to default to postgres://mypa:mypa_password@localhost:5432/mypa —
// the compose Postgres published on the host, i.e. the live database. Every `go test ./...` wrote
// fixture users into it and ran AutoMigrate against the schema the migrate service owns.
// Tests must get a database from NewDatabase instead; this guard keeps it that way.
var liveDBPatterns = []*regexp.Regexp{
	// The compose database name; config tests may still parse unrelated localhost DSNs.
	regexp.MustCompile(`(localhost|127\.0\.0\.1):5432/mypa\b`),
	regexp.MustCompile(`os\.Getenv\("DATABASE_URL"\)`),
}

func TestNoTestReachesTheLiveDatabase(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dashboard":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "guard_test.go" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, re := range liveDBPatterns {
			assert.False(t, re.Match(src), "%s matches %q: use pgtest.NewDatabase(t), never the compose database", path, re.String())
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, scanned, 50, "the walk must actually reach the repository's test files")
}
