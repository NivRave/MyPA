// Package packs defines the feature packs a household can switch on (V4-27, DE-6).
//
// Core is always on: memory, reminders, calendar, tasks and contacts, web search, undo, family
// sharing, user preferences and the personal CRM. Every other feature belongs to one opt-in pack.
// A disabled pack contributes no tools, prompt section, routing keywords, dashboard page or
// briefing lines; its data is kept. There are no per-tool switches.
//
// The package is a leaf: it names modules, topics and pages as plain strings, so identity, the
// orchestrator, the GraphQL layer and the scheduler can all depend on it.
package packs

import (
	"sort"
	"strings"
)

// ID names a pack. It is what a household stores.
type ID string

// The opt-in packs.
const (
	Baby        ID = "baby"
	House       ID = "house"
	Finance     ID = "finance"
	Gmail       ID = "gmail"
	Automations ID = "automations"
)

// Pack is one opt-in pack.
type Pack struct {
	ID          ID
	Name        string
	Description string
	// Modules are the ToolModule names (orchestrator.ToolModule.Name) the pack owns.
	Modules []string
	// Topics are the topics the pack adds to the household's topic list.
	Topics []string
	// Pages are the dashboard routes the pack owns.
	Pages []string
}

// catalogue lists every pack in the order the dashboard shows them.
var catalogue = []Pack{
	{ID: Baby, Name: "Baby", Description: "Baby care logs, growth, milestones, medication cooldowns and supplies.",
		Modules: []string{"baby"}, Topics: []string{"baby"}, Pages: []string{"/baby"}},
	{ID: House, Name: "House and vehicles", Description: "Contracts and warranties, home maintenance, and vehicle tests, insurance and service.",
		Modules: []string{"house"}, Topics: []string{"house"}, Pages: []string{"/house"}},
	{ID: Finance, Name: "Finance", Description: "Expenses and income, category budgets with alerts, and recurring bills.",
		Modules: []string{"finance"}, Topics: []string{"finance"}, Pages: []string{"/finance"}},
	{ID: Gmail, Name: "Gmail", Description: "Search, read, label, archive and draft replies to email in the connected Google account.",
		Modules: []string{"workspace_gmail"}},
	{ID: Automations, Name: "Automations", Description: "External APIs, event and schedule rules, and scheduled workflows.",
		Modules: []string{"automation"}, Pages: []string{"/automations"}},
}

// CoreTopics are the topics every household has, whatever its packs.
var CoreTopics = []string{"family", "health", "personal", "work"}

// All returns every pack, in display order.
func All() []Pack {
	out := make([]Pack, len(catalogue))
	copy(out, catalogue)
	return out
}

// Lookup returns the pack with this ID (trimmed, any case).
func Lookup(id string) (Pack, bool) {
	want := ID(strings.ToLower(strings.TrimSpace(id)))
	for _, p := range catalogue {
		if p.ID == want {
			return p, true
		}
	}
	return Pack{}, false
}

// OfModule returns the pack that owns the module. ok is false for a Core module.
func OfModule(module string) (id ID, ok bool) {
	for _, p := range catalogue {
		for _, m := range p.Modules {
			if m == module {
				return p.ID, true
			}
		}
	}
	return "", false
}

// OfPage returns the pack that owns the dashboard route (the route or a page below it).
func OfPage(route string) (id ID, ok bool) {
	for _, p := range catalogue {
		for _, pg := range p.Pages {
			if route == pg || strings.HasPrefix(route, pg+"/") {
				return p.ID, true
			}
		}
	}
	return "", false
}

// Set is the packs a household has switched on. The zero value is Core only.
type Set struct {
	on map[ID]bool
}

// Parse reads a stored pack list (comma-separated IDs). Unknown names are ignored, so a pack
// removed from the catalogue never breaks a household.
func Parse(stored string) Set {
	s := Set{on: map[ID]bool{}}
	for _, part := range strings.Split(stored, ",") {
		if p, ok := Lookup(part); ok {
			s.on[p.ID] = true
		}
	}
	return s
}

// Everything is every pack switched on. It stands for "no household" (tests and generated docs),
// never for a household: a household's set always comes from what it stored.
func Everything() Set {
	s := Set{on: map[ID]bool{}}
	for _, p := range catalogue {
		s.on[p.ID] = true
	}
	return s
}

// Has reports whether the pack is switched on.
func (s Set) Has(id ID) bool { return s.on[id] }

// With returns a copy of the set with the pack switched on or off.
func (s Set) With(id ID, on bool) Set {
	out := Set{on: map[ID]bool{}}
	for k, v := range s.on {
		out.on[k] = v
	}
	if on {
		out.on[id] = true
	} else {
		delete(out.on, id)
	}
	return out
}

// IDs returns the switched-on packs in catalogue order.
func (s Set) IDs() []ID {
	var out []ID
	for _, p := range catalogue {
		if s.on[p.ID] {
			out = append(out, p.ID)
		}
	}
	return out
}

// String is the stored form: the switched-on IDs, sorted, comma-separated ("" = Core only).
func (s Set) String() string {
	ids := make([]string, 0, len(s.on))
	for id := range s.on {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// ModuleEnabled reports whether a ToolModule may serve the household: Core modules always, a
// pack's modules only while the pack is on.
func (s Set) ModuleEnabled(module string) bool {
	id, ok := OfModule(module)
	return !ok || s.on[id]
}

// PageEnabled reports whether a dashboard route may be shown: Core pages always, a pack's pages
// only while the pack is on.
func (s Set) PageEnabled(route string) bool {
	id, ok := OfPage(route)
	return !ok || s.on[id]
}

// Topics is the household's topic list: the Core topics, then each switched-on pack's topics.
func (s Set) Topics() []string {
	out := append([]string(nil), CoreTopics...)
	for _, p := range catalogue {
		if s.on[p.ID] {
			out = append(out, p.Topics...)
		}
	}
	return out
}

// HasTopic reports whether the topic is on the household's topic list.
func (s Set) HasTopic(topic string) bool {
	t := strings.ToLower(strings.TrimSpace(topic))
	for _, have := range s.Topics() {
		if have == t {
			return true
		}
	}
	return false
}
