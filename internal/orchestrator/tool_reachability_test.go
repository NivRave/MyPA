package orchestrator_test

import (
	"testing"

	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/toolcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allProductionModules is the single list of production modules (kept in sync with
// cmd/orchestrator/main.go by toolcatalog.TestProductionModulesMatchOrchestratorMain).
func allProductionModules() []orchestrator.ToolModule { return toolcatalog.ProductionModules() }

// Every registered tool must be offered to the model in at least one turn: either it is a
// Tier-1 core tool, or its module maps to a domain the intent matcher can actually select.
func TestEveryRegisteredToolIsReachable(t *testing.T) {
	matcher := orchestrator.NewKeywordIntentMatcher()
	routable := map[orchestrator.DomainGroup]bool{}
	for _, d := range matcher.SupportedDomains() {
		routable[d] = true
	}

	total := 0
	for _, m := range allProductionModules() {
		domain, mapped := orchestrator.ModuleDomainMapping[m.Name()]
		for _, decl := range m.Declarations() {
			require.NotNil(t, decl)
			total++
			if orchestrator.CoreToolNames[decl.Name] {
				continue
			}
			assert.True(t, mapped, "tool %q: module %q has no ModuleDomainMapping entry and the tool is not core", decl.Name, m.Name())
			if mapped {
				assert.True(t, routable[domain], "tool %q: domain %q has no intent-matcher keywords", decl.Name, domain)
			}
		}
	}
	assert.Greater(t, total, 50, "sanity: expected the full production tool catalogue")
}

// The previously unreachable tools must now be selected for natural requests (EN + HE).
func TestPreviouslyUnreachableToolsAreSelected(t *testing.T) {
	reg := orchestrator.NewRegistry()
	for _, m := range allProductionModules() {
		reg.Register(m)
	}
	matcher := orchestrator.NewKeywordIntentMatcher()

	cases := []struct {
		text string
		tool string
	}{
		{"what do you remember about my mom?", "get_entity_profile"},
		{"update Dana's profile: she is allergic to peanuts", "upsert_entity_profile"},
		{"מה אתה זוכר על סבתא?", "get_entity_profile"},
		{"תעדכן את הפרופיל של דנה", "upsert_entity_profile"},
		{"show me the recent changes", "list_recent_changes"},
		{"what changed this week?", "list_recent_changes"},
		{"export all my finance data", "export_topic_data"},
		{"תראה לי את היסטוריית השינויים", "list_recent_changes"},
		{"ייצוא נתוני הבית", "export_topic_data"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			names := map[string]bool{}
			for _, d := range reg.SelectDeclarations(matcher.MatchDomains(tc.text), false) {
				names[d.Name] = true
			}
			assert.True(t, names[tc.tool], "%q must offer %s; matched domains: %v", tc.text, tc.tool, matcher.MatchDomains(tc.text))
		})
	}
}
