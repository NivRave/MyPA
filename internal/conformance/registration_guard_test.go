package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const channelImportPath = "github.com/nivik/mypa/internal/channel"

// registryBuilder is the one production file allowed to create a channel registry: the list of
// shipped adapters, which seals what it builds.
const registryBuilder = "internal/channel/adapters/adapters.go"

// TestAdaptersReachTheAppOnlyThroughBuild is the other half of "a new adapter cannot be
// registered without passing" (V4-34). The suite runs every adapter of adapters.Build, and that
// registry is sealed (TestFixturesMatchRegisteredAdapters checks both). What remains is a second
// registry built somewhere else, so no production Go file but adapters.go may call
// channel.NewRegistry. The guard parses every production file and resolves the import name, so an
// alias or a dot import does not hide the call; it fails if it reads too few files to be seeing
// the tree.
func TestAdaptersReachTheAppOnlyThroughBuild(t *testing.T) {
	root := moduleRootDir(t)
	var offenders []string
	files, builderCalls := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".next", "vendor", "testdata", "dashboard":
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
		if strings.HasPrefix(rel, "internal/channel/") && !strings.HasPrefix(rel, "internal/channel/adapters/") {
			return nil // the channel package itself defines NewRegistry
		}
		files++
		calls, err := newRegistryCalls(path)
		if err != nil {
			return err
		}
		for _, line := range calls {
			if rel == registryBuilder {
				builderCalls++
				continue
			}
			offenders = append(offenders, rel+":"+strconv.Itoa(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	if files < 100 {
		t.Fatalf("parsed only %d production Go files: the walk is not seeing the tree", files)
	}
	if builderCalls != 1 {
		t.Fatalf("%s calls channel.NewRegistry %d times, expected once: the guard's allowlist is stale", registryBuilder, builderCalls)
	}
	if len(offenders) > 0 {
		t.Errorf("channel.NewRegistry is called outside %s, so an adapter could reach the app without the conformance suite; "+
			"add the adapter to adapters.Build instead:\n%s", registryBuilder, strings.Join(offenders, "\n"))
	}
}

// newRegistryCalls returns the lines of a file that call NewRegistry from the channel package,
// whatever name the file imports it under.
func newRegistryCalls(path string) ([]int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local, dot := "", false
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != channelImportPath {
			continue
		}
		local = "channel"
		if imp.Name != nil {
			local, dot = imp.Name.Name, imp.Name.Name == "."
		}
	}
	if local == "" || local == "_" {
		return nil, nil
	}
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if x, ok := fn.X.(*ast.Ident); ok && !dot && x.Name == local && fn.Sel.Name == "NewRegistry" {
				lines = append(lines, fset.Position(call.Pos()).Line)
			}
		case *ast.Ident:
			if dot && fn.Name == "NewRegistry" {
				lines = append(lines, fset.Position(call.Pos()).Line)
			}
		}
		return true
	})
	return lines, nil
}

// TestNewRegistryCallsSeesEveryForm keeps the guard's parser honest.
func TestNewRegistryCallsSeesEveryForm(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]int{
		"package a\nimport \"" + channelImportPath + "\"\nvar r = channel.NewRegistry()\n":                1,
		"package a\nimport layer \"" + channelImportPath + "\"\nvar r = layer.NewRegistry()\n":            1,
		"package a\nimport . \"" + channelImportPath + "\"\nvar r = NewRegistry()\n":                      1,
		"package a\nimport \"" + channelImportPath + "\"\nfunc f() { _ = channel.NewRegistry() }\n":       1,
		"package a\nimport \"other/channel\"\nvar r = channel.NewRegistry()\n":                            0,
		"package a\nimport \"" + channelImportPath + "\"\nvar r = orchestrator.NewRegistry()\n":           0,
		"package a\nimport \"" + channelImportPath + "\"\nvar a channel.Adapter\nvar r = NewRegistry()\n": 0,
	}
	i := 0
	for src, want := range cases {
		i++
		path := filepath.Join(dir, "f"+strconv.Itoa(i)+".go")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := newRegistryCalls(path)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(got) != want {
			t.Errorf("%q: found %d calls, want %d", src, len(got), want)
		}
	}
}

func moduleRootDir(t *testing.T) string {
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
