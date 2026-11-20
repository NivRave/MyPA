package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-27: every Query and Mutation field is either a pack's (refused while the pack is off) or
// Core, never neither, so a field added for a pack cannot skip the guard by being forgotten.
func TestPackGuard_EveryRootFieldIsClassified(t *testing.T) {
	sch := NewExecutableSchema(Config{Resolvers: &Resolver{}}).Schema()
	seen := map[string]bool{}
	for _, typ := range []string{"Query", "Mutation"} {
		def := sch.Types[typ]
		require.NotNil(t, def, typ)
		for _, f := range def.Fields {
			if f.Name == "__schema" || f.Name == "__type" || f.Name == "__typename" {
				continue
			}
			seen[f.Name] = true
			_, pack := packFields[f.Name]
			assert.True(t, pack != coreFields[f.Name], "%s.%s must be either a pack's field or Core", typ, f.Name)
		}
	}
	for name := range packFields {
		assert.True(t, seen[name], "packFields names %s, which the schema does not have", name)
	}
	for name := range coreFields {
		assert.True(t, seen[name], "coreFields names %s, which the schema does not have", name)
	}
}
