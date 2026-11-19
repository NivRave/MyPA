package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoRoot = "../.."

// The committed docs must equal their regenerated form. (Moved here from internal/toolcatalog
// when the channel matrix joined the generated regions, V4-34.)
func TestDocsAreUpToDate(t *testing.T) {
	regions, err := Regions()
	require.NoError(t, err)
	for _, name := range DocFiles {
		raw, err := os.ReadFile(filepath.Join(repoRoot, name))
		require.NoError(t, err)
		want, err := Apply(string(raw), regions)
		require.NoError(t, err, name)
		assert.True(t, want == string(raw), "%s is stale: run `go run ./cmd/devtools/gendocs`", name)
	}
	system, err := os.ReadFile(filepath.Join(repoRoot, "SYSTEM.md"))
	require.NoError(t, err)
	for _, region := range []string{"tool-catalog", "tool-tiers", ChannelMatrixRegion} {
		assert.True(t, strings.Contains(string(system), "<!-- BEGIN GENERATED: "+region+" -->"), "SYSTEM.md lost its %s region", region)
	}
}

// The matrix has one column per registered adapter, so a new adapter appears in SYSTEM.md (and
// TestDocsAreUpToDate fails until the docs are regenerated).
func TestChannelMatrixHasAColumnPerRegisteredAdapter(t *testing.T) {
	reg, err := adapters.Build(adapters.Config{})
	require.NoError(t, err)
	matrix, err := ChannelMatrix()
	require.NoError(t, err)
	header := strings.SplitN(matrix, "\n", 2)[0]
	require.NotEmpty(t, reg.All())
	for _, a := range reg.All() {
		assert.Contains(t, header, "`"+a.Name()+"`")
	}
	assert.Equal(t, len(reg.All())+3, strings.Count(header, "|"), "a capability column, one per adapter and a fallback column")
}
