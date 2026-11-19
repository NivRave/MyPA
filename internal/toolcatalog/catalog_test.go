package toolcatalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoRoot = "../.."

func TestApply_BlockAndInline(t *testing.T) {
	regions := map[string]string{"blk": "line1\nline2\n", "n": "42"}
	doc := "a\n<!-- BEGIN GENERATED: blk -->\nstale\n<!-- END GENERATED: blk -->\nwe have <!-- gen:n -->7<!-- /gen:n --> tools\n"
	got, err := Apply(doc, regions)
	require.NoError(t, err)
	assert.Equal(t, "a\n<!-- BEGIN GENERATED: blk -->\nline1\nline2\n<!-- END GENERATED: blk -->\nwe have <!-- gen:n -->42<!-- /gen:n --> tools\n", got)

	again, err := Apply(got, regions)
	require.NoError(t, err)
	assert.Equal(t, got, again, "Apply must be idempotent")
}

func TestApply_PreservesCRLF(t *testing.T) {
	doc := "x\r\n<!-- BEGIN GENERATED: blk -->\r\nold\r\n<!-- END GENERATED: blk -->\r\n"
	got, err := Apply(doc, map[string]string{"blk": "a\nb"})
	require.NoError(t, err)
	assert.Equal(t, "x\r\n<!-- BEGIN GENERATED: blk -->\r\na\r\nb\r\n<!-- END GENERATED: blk -->\r\n", got)
}

func TestApply_Errors(t *testing.T) {
	_, err := Apply("<!-- BEGIN GENERATED: nope -->\n<!-- END GENERATED: nope -->", map[string]string{})
	assert.ErrorContains(t, err, `unknown generated region "nope"`)
	_, err = Apply("<!-- BEGIN GENERATED: blk -->\nno end", map[string]string{"blk": "x"})
	assert.ErrorContains(t, err, "no END marker")
	_, err = Apply("<!-- gen:n -->1", map[string]string{"n": "2"})
	assert.ErrorContains(t, err, "no closing marker")
}

func TestBuild_Sanity(t *testing.T) {
	c := Build()
	seen := map[string]bool{}
	for _, g := range c.Groups {
		for _, tool := range g.Tools {
			assert.False(t, seen[tool.Name], "duplicate tool %q", tool.Name)
			seen[tool.Name] = true
		}
	}
	assert.True(t, seen["get_current_time"], "registry built-in must be catalogued")
	assert.Equal(t, len(ProductionModules()), c.ModuleCount())
	assert.Greater(t, c.ToolCount(), 50)
	for _, ct := range c.CoreTools() {
		assert.NotEmpty(t, ct[1], "core tool %q has no owner", ct[0])
	}
}

// ProductionModules must mirror the modules wired in the orchestrator, or the docs (and the
// reachability test that reuses this list) silently miss tools.
func TestProductionModulesMatchOrchestratorMain(t *testing.T) {
	path := filepath.Join(repoRoot, "cmd", "orchestrator", "main.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)

	domainImports := map[string]bool{} // local package names of internal/domains/* imports
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if strings.Contains(p, "/internal/domains/") {
			name := p[strings.LastIndex(p, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			domainImports[name] = true
		}
	}
	var wired []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewModule" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && domainImports[id.Name] {
			wired = append(wired, id.Name)
		}
		return true
	})

	var listed []string
	for _, m := range ProductionModules() {
		listed = append(listed, modulePackage(m))
	}
	sort.Strings(wired)
	sort.Strings(listed)
	assert.Equal(t, wired, listed, "toolcatalog.ProductionModules is out of sync with cmd/orchestrator/main.go")
}

// The committed docs must equal their regenerated form.
func TestDocsAreUpToDate(t *testing.T) {
	regions := Build().Regions()
	for _, name := range DocFiles {
		raw, err := os.ReadFile(filepath.Join(repoRoot, name))
		require.NoError(t, err)
		want, err := Apply(string(raw), regions)
		require.NoError(t, err, name)
		assert.True(t, want == string(raw), "%s is stale: run `go run ./cmd/devtools/gendocs`", name)
	}
	system, err := os.ReadFile(filepath.Join(repoRoot, "SYSTEM.md"))
	require.NoError(t, err)
	for _, region := range []string{"tool-catalog", "tool-tiers"} {
		assert.True(t, strings.Contains(string(system), "<!-- BEGIN GENERATED: "+region+" -->"), "SYSTEM.md lost its %s region", region)
	}
}

// V4-06 "done when": remember_fact is gone. No production module declares it, the static tool
// list does not, it is not a core tool and not in the confirmation gate, and the generated
// catalogue does not list it. save_topic_memory, which replaces it, keeps both protections the
// removed tool had: it is always offered, and it needs confirmation in an untrusted turn.
func TestRememberFactIsGone(t *testing.T) {
	const gone, kept = "remember_fact", "save_topic_memory"

	declaredKept := false
	for _, m := range ProductionModules() {
		for _, d := range m.Declarations() {
			assert.NotEqual(t, gone, d.Name, "module %s still declares %s", m.Name(), gone)
			if d.Name == kept {
				declaredKept = true
			}
		}
	}
	assert.True(t, declaredKept, "%s must be declared by a production module", kept)
	for _, d := range llm.CalendarEventTool.FunctionDeclarations {
		assert.NotEqual(t, gone, d.Name, "the static tool list still declares %s", gone)
	}

	assert.False(t, orchestrator.CoreToolNames[gone])
	assert.False(t, orchestrator.RequiresConfirmationWhenUntrusted(gone))
	assert.True(t, orchestrator.CoreToolNames[kept], "%s must always be offered", kept)
	assert.True(t, orchestrator.RequiresConfirmationWhenUntrusted(kept), "%s must stay gated in an untrusted turn", kept)

	c := Build()
	assert.NotContains(t, c.RenderCatalog(), gone)
	assert.NotContains(t, c.RenderTiers(), gone)
	for _, tool := range c.CoreTools() {
		assert.NotEqual(t, gone, tool[0])
	}
}
