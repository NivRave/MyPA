package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

// DomainGroups returns every Tier-2 domain a module maps to, sorted: the names LLM_LOCAL_DOMAINS
// accepts.
func DomainGroups() []DomainGroup {
	seen := map[DomainGroup]bool{}
	var out []DomainGroup
	for _, d := range ModuleDomainMapping {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ParseDomainGroups checks the names in LLM_LOCAL_DOMAINS (V4-02). An unknown name is an error, so
// a typo cannot silently send a turn meant to stay local to a cloud provider.
func ParseDomainGroups(names []string) ([]DomainGroup, error) {
	known := map[DomainGroup]bool{}
	var all []string
	for _, d := range DomainGroups() {
		known[d] = true
		all = append(all, string(d))
	}
	var out []DomainGroup
	for _, n := range names {
		d := DomainGroup(strings.ToLower(strings.TrimSpace(n)))
		if !known[d] {
			return nil, fmt.Errorf("LLM_LOCAL_DOMAINS names an unknown domain %q; the domains are %s", n, strings.Join(all, ", "))
		}
		out = append(out, d)
	}
	return out, nil
}

// WithLocalDomains sets the domains whose turns run only on the local model (LLM_LOCAL_DOMAINS,
// V4-02). A turn is kept local when one of its domains, matched by its text or carried over from
// the turn before (sticky), is in the list: every model call of the turn goes to the local
// provider only, and the turn skips the memory search by meaning, which would send its text to
// the embedding model.
func (e *Engine) WithLocalDomains(domains []DomainGroup) *Engine {
	e.localDomains = map[DomainGroup]bool{}
	for _, d := range domains {
		e.localDomains[d] = true
	}
	return e
}

// keptLocal reports whether a turn with these domains is kept on the local model.
func (e *Engine) keptLocal(domains []DomainGroup) bool {
	for _, d := range domains {
		if e.localDomains[d] {
			return true
		}
	}
	return false
}
