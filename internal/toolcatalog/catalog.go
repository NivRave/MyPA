// Package toolcatalog renders the LLM tool catalogue documentation from the live ToolModule
// declarations (P5-5), so SYSTEM.md / README.md never carry hand-maintained tool counts.
//
// Regenerate with `go run ./cmd/devtools/gendocs`; TestDocsAreUpToDate (internal/docgen) fails when the
// committed docs drift from the code.
package toolcatalog

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/domains/house"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/domains/preferences"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// ProductionModules returns one instance of every module registered in
// cmd/mypa/worker.go, in registration order. Dependencies are nil: only Name() and
// Declarations() may be called, and neither touches dependencies.
// TestProductionModulesMatchTheWorkerWiring keeps this list in sync with worker.go.
func ProductionModules() []orchestrator.ToolModule {
	return []orchestrator.ToolModule{
		workspace_gmail.NewModule(),
		workspace_tasks.NewModule(),
		workspace_contacts.NewModule(),
		web_research.NewModule(),
		durability.NewModule(nil),
		memory.NewModule(nil),
		shared.NewModule(nil, nil, nil),
		schedule.NewModule(nil),
		baby.NewModule(nil),
		house.NewModule(nil),
		finance.NewModule(nil),
		automation.NewModule(nil),
		preferences.NewModule(nil),
	}
}

// modulePackage returns the Go package name a module type lives in (e.g. "workspace_gmail").
func modulePackage(m orchestrator.ToolModule) string {
	t := reflect.TypeOf(m)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	p := t.PkgPath()
	return p[strings.LastIndex(p, "/")+1:]
}

// builtinModule names the group of tools the Registry injects itself.
const builtinModule = "registry"

// Group is one section of the catalogue: a module (or the registry's built-ins) and its tools.
type Group struct {
	Module string
	Domain orchestrator.DomainGroup // empty for built-ins
	Tools  []*genai.FunctionDeclaration
}

// Catalog is the full set of tools offered to the LLM.
type Catalog struct {
	Groups []Group
}

// Build collects every tool from the production modules plus registry built-ins
// (tools the Registry injects itself, e.g. get_current_time).
func Build() Catalog {
	reg := orchestrator.NewRegistry()
	var c Catalog
	fromModules := map[string]bool{}
	for _, m := range ProductionModules() {
		reg.Register(m)
		g := Group{Module: m.Name(), Domain: orchestrator.ModuleDomainMapping[m.Name()]}
		for _, d := range m.Declarations() {
			if d != nil && d.Name != "" && !fromModules[d.Name] {
				fromModules[d.Name] = true
				g.Tools = append(g.Tools, d)
			}
		}
		c.Groups = append(c.Groups, g)
	}
	builtin := Group{Module: builtinModule}
	for _, d := range reg.CoreDeclarations() {
		if !fromModules[d.Name] {
			builtin.Tools = append(builtin.Tools, d)
		}
	}
	if len(builtin.Tools) > 0 {
		c.Groups = append([]Group{builtin}, c.Groups...)
	}
	return c
}

// ToolCount is the number of distinct tools.
func (c Catalog) ToolCount() int {
	n := 0
	for _, g := range c.Groups {
		n += len(g.Tools)
	}
	return n
}

// ModuleCount is the number of ToolModules (built-ins excluded).
func (c Catalog) ModuleCount() int {
	n := 0
	for _, g := range c.Groups {
		if g.Module != builtinModule {
			n++
		}
	}
	return n
}

// CoreTools lists the Tier-1 tools in catalogue order, with their owning module.
func (c Catalog) CoreTools() [][2]string {
	var out [][2]string
	for _, g := range c.Groups {
		for _, t := range g.Tools {
			if orchestrator.CoreToolNames[t.Name] {
				out = append(out, [2]string{t.Name, g.Module})
			}
		}
	}
	return out
}

// Summary is the inline count used in prose, e.g. "70 tools across 13 modules".
func (c Catalog) Summary() string {
	return fmt.Sprintf("%d tools across %d modules", c.ToolCount(), c.ModuleCount())
}

// RenderCatalog renders the per-module tool tables (SYSTEM.md section 6 body).
func (c Catalog) RenderCatalog() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s. Tier 1 = always offered to the model; Tier 2 = offered when the intent matcher selects the module's domain (see §3.2).\n", c.Summary())
	for i, g := range c.Groups {
		title := fmt.Sprintf("`%s`", g.Module)
		if g.Module == builtinModule {
			title = "Registry built-ins"
		}
		fmt.Fprintf(&b, "\n### 6.%d %s (%s)\n\n", i+1, title, tools(len(g.Tools)))
		b.WriteString("| Tool | Tier | Parameters (`*` = required) | Description |\n")
		b.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, t := range g.Tools {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", t.Name, tier(t.Name, g.Domain), renderParams(t.Parameters), cell(t.Description))
		}
	}
	return b.String()
}

// RenderTiers renders the Tier-1 list and the Tier-2 domain groups (SYSTEM.md section 3.2).
func (c Catalog) RenderTiers() string {
	var b strings.Builder
	core := c.CoreTools()
	fmt.Fprintf(&b, "1. **Tier 1 Core Tools (%s):** always injected into every user turn: ", tools(len(core)))
	for i, ct := range core {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "`%s` (`%s`)", ct[0], ct[1])
	}
	b.WriteString(".\n2. **Tier 2 Domain Groups** (injected on demand via `KeywordIntentMatcher`):\n")
	byDomain := map[orchestrator.DomainGroup][]string{}
	var order []orchestrator.DomainGroup
	for _, g := range c.Groups {
		for _, t := range g.Tools {
			if orchestrator.CoreToolNames[t.Name] || g.Domain == "" {
				continue
			}
			if _, ok := byDomain[g.Domain]; !ok {
				order = append(order, g.Domain)
			}
			byDomain[g.Domain] = append(byDomain[g.Domain], "`"+t.Name+"`")
		}
	}
	for _, d := range order {
		fmt.Fprintf(&b, "   - `%s` (%s): %s.\n", d, tools(len(byDomain[d])), strings.Join(byDomain[d], ", "))
	}
	return b.String()
}

// tools formats a tool count with the right plural.
func tools(n int) string {
	if n == 1 {
		return "1 tool"
	}
	return fmt.Sprintf("%d tools", n)
}

func tier(name string, domain orchestrator.DomainGroup) string {
	if orchestrator.CoreToolNames[name] {
		return "1 (core)"
	}
	if domain == "" {
		return "—"
	}
	return fmt.Sprintf("2 (`%s`)", domain)
}

// renderParams lists parameters: required ones first (declared order), then the rest sorted.
func renderParams(s *genai.Schema) string {
	if s == nil || len(s.Properties) == 0 {
		return "—"
	}
	required := map[string]bool{}
	var names []string
	for _, r := range s.Required {
		if _, ok := s.Properties[r]; ok && !required[r] {
			required[r] = true
			names = append(names, r)
		}
	}
	var optional []string
	for n := range s.Properties {
		if !required[n] {
			optional = append(optional, n)
		}
	}
	sort.Strings(optional)
	names = append(names, optional...)

	parts := make([]string, 0, len(names))
	for _, n := range names {
		p := "`" + n + "`"
		if required[n] {
			p += "*"
		}
		if t := typeName(s.Properties[n]); t != "" {
			p += " (" + t + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

func typeName(s *genai.Schema) string {
	if s == nil {
		return ""
	}
	t := strings.ToLower(string(s.Type))
	if s.Type == genai.TypeArray && s.Items != nil {
		return "[]" + strings.ToLower(string(s.Items.Type))
	}
	return t
}

// cell makes free text safe for a single Markdown table cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// ---- marker-delimited regions -------------------------------------------------------------

// Regions maps a marker name to its generated content. Block markers wrap whole lines:
//
//	<!-- BEGIN GENERATED: name -->
//	...
//	<!-- END GENERATED: name -->
//
// Inline markers wrap text within a line: <!-- gen:name -->...<!-- /gen:name -->.
func (c Catalog) Regions() map[string]string {
	return map[string]string{
		"tool-catalog": c.RenderCatalog(),
		"tool-tiers":   c.RenderTiers(),
		"tool-summary": c.Summary(),
		"tool-count":   fmt.Sprintf("%d", c.ToolCount()),
		"module-count": fmt.Sprintf("%d", c.ModuleCount()),
	}
}

// Apply replaces every marker region in doc with its generated content. Line endings of the
// document are preserved. It errors on an unknown marker name or an unterminated region.
func Apply(doc string, regions map[string]string) (string, error) {
	eol := "\n"
	if strings.Contains(doc, "\r\n") {
		eol = "\r\n"
	}
	out, err := applyBlocks(doc, regions, eol)
	if err != nil {
		return "", err
	}
	return applyInline(out, regions)
}

func applyBlocks(doc string, regions map[string]string, eol string) (string, error) {
	const begin, end = "<!-- BEGIN GENERATED: ", "<!-- END GENERATED: "
	var b strings.Builder
	rest := doc
	for {
		i := strings.Index(rest, begin)
		if i < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		nameEnd := strings.Index(rest[i:], " -->")
		if nameEnd < 0 {
			return "", fmt.Errorf("malformed BEGIN GENERATED marker")
		}
		name := rest[i+len(begin) : i+nameEnd]
		content, ok := regions[name]
		if !ok {
			return "", fmt.Errorf("unknown generated region %q", name)
		}
		openEnd := i + nameEnd + len(" -->")
		closeTag := end + name + " -->"
		j := strings.Index(rest[openEnd:], closeTag)
		if j < 0 {
			return "", fmt.Errorf("region %q has no END marker", name)
		}
		b.WriteString(rest[:openEnd])
		b.WriteString(eol)
		b.WriteString(strings.ReplaceAll(strings.TrimRight(content, "\n"), "\n", eol))
		b.WriteString(eol)
		b.WriteString(closeTag)
		rest = rest[openEnd+j+len(closeTag):]
	}
}

func applyInline(doc string, regions map[string]string) (string, error) {
	const open = "<!-- gen:"
	var b strings.Builder
	rest := doc
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		nameEnd := strings.Index(rest[i:], " -->")
		if nameEnd < 0 {
			return "", fmt.Errorf("malformed inline gen marker")
		}
		name := rest[i+len(open) : i+nameEnd]
		content, ok := regions[name]
		if !ok {
			return "", fmt.Errorf("unknown generated region %q", name)
		}
		openEnd := i + nameEnd + len(" -->")
		closeTag := "<!-- /gen:" + name + " -->"
		j := strings.Index(rest[openEnd:], closeTag)
		if j < 0 {
			return "", fmt.Errorf("inline region %q has no closing marker", name)
		}
		b.WriteString(rest[:openEnd])
		b.WriteString(content)
		b.WriteString(closeTag)
		rest = rest[openEnd+j+len(closeTag):]
	}
}

// DocFiles are the repository files (relative to the module root) containing generated regions.
var DocFiles = []string{"SYSTEM.md", "README.md"}
