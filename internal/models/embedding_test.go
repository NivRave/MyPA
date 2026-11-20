package models

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-03: moving to an embedding model of another size is a documented code change. The checklist
// in docs/EMBEDDINGS.md names every production file that writes the size; this test keeps it
// complete, so following it cannot leave one place on the old size.
//
// It walks the tree itself and must not skip: a guard that skips looks like one that passes.
func TestEmbeddingDimensions_WrittenOnlyWhereTheChecklistSays(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	doc, err := os.ReadFile(filepath.Join(root, "docs", "EMBEDDINGS.md"))
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)<!-- size-checklist -->(.*?)<!-- /size-checklist -->`).FindSubmatch(doc)
	require.NotNil(t, block, "docs/EMBEDDINGS.md must hold the size checklist between its markers")
	var listed []string
	for _, m := range regexp.MustCompile("(?m)^\\s*- `([^`]+)`").FindAllSubmatch(block[1], -1) {
		listed = append(listed, string(m[1]))
	}
	sort.Strings(listed)
	require.NotEmpty(t, listed)

	size := regexp.MustCompile(`\b` + strconv.Itoa(EmbeddingDimensions) + `\b`)
	skip := map[string]bool{".git": true, "node_modules": true, ".next": true, "docs": true, ".claude": true, ".agents": true, "backups": true}
	var found []string
	searched := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		production := (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || strings.HasSuffix(name, ".sql")
		if !production {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		searched++
		if size.Match(data) {
			found = append(found, name)
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, searched, 100, "the walk must cover the source tree")
	sort.Strings(found)

	assert.Equal(t, listed, found, "the files that write the embedding size (%d) must be exactly the checklist in docs/EMBEDDINGS.md: "+
		"use models.EmbeddingDimensions in new code, or add the file to the checklist", EmbeddingDimensions)
}
