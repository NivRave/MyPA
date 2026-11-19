package toolcatalog

import (
	"strings"
	"testing"

	"google.golang.org/genai"
)

// ADR 0002 Decision 3 (V4-30): the household is never taken from tool arguments. A tool parameter
// that names a household, family or tenant would let the model (or text injected into the turn)
// pick one; the scope comes only from the verified sender.
func TestToolDeclarations_NoParameterNamesATenant(t *testing.T) {
	forbidden := []string{"household", "family", "tenant"}
	var walk func(tool, path string, s *genai.Schema)
	walk = func(tool, path string, s *genai.Schema) {
		if s == nil {
			return
		}
		for name, prop := range s.Properties {
			lower := strings.ToLower(name)
			for _, f := range forbidden {
				if strings.Contains(lower, f) {
					t.Errorf("tool %s: parameter %s%s names a %s; the household comes from the scope, never from arguments", tool, path, name, f)
				}
			}
			walk(tool, path+name+".", prop)
		}
		walk(tool, path+"[]", s.Items)
	}
	count := 0
	for _, m := range ProductionModules() {
		for _, d := range m.Declarations() {
			if d == nil {
				continue
			}
			count++
			walk(d.Name, "", d.Parameters)
		}
	}
	if count < 50 {
		t.Fatalf("sanity: only %d tool declarations were walked", count)
	}
}
