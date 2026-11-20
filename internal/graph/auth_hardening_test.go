package graph

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// householdArgument matches an argument name that would let a client pick a tenant.
var householdArgument = regexp.MustCompile(`(?i)family|household|tenant`)

// ADR 0002 Decision 3 (V4-35): the household comes from the session, never from the client. A
// test fails if any field of the executable schema takes an argument that names a household, a
// family or a tenant.
func TestSchema_NoFieldTakesAHousehold(t *testing.T) {
	schema := NewExecutableSchema(Config{Resolvers: &Resolver{}}).Schema()
	require.NotNil(t, schema)
	fields := 0
	for name, def := range schema.Types {
		if strings.HasPrefix(name, "__") {
			continue
		}
		for _, f := range def.Fields {
			fields++
			for _, a := range f.Arguments {
				assert.False(t, householdArgument.MatchString(a.Name), "%s.%s takes %q: the household comes from the session (V4-35)", name, f.Name, a.Name)
			}
		}
	}
	assert.Greater(t, fields, 100, "the walk must reach the schema's fields")
	require.NotNil(t, schema.Query)
	require.NotNil(t, schema.Mutation)
	assert.NotNil(t, schema.Query.Fields.ForName("me"), "the signed-in person is a query")
	assert.NotNil(t, schema.Mutation.Fields.ForName("signOut"))
}

// The guard above must keep catching what it is for: the schema file had familyGroup arguments
// before V4-35, and the pattern matches each spelling.
func TestSchema_GuardMatchesTheOldArguments(t *testing.T) {
	for _, name := range []string{"familyGroup", "householdId", "tenant", "FamilyGroup"} {
		assert.True(t, householdArgument.MatchString(name), name)
	}
	src, err := os.ReadFile("schema.graphqls")
	require.NoError(t, err)
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, `"`) {
			continue
		}
		i, j := strings.Index(line, "("), strings.Index(line, ")")
		if i >= 0 && j > i {
			assert.False(t, householdArgument.MatchString(line[i:j]), "schema.graphqls: %s", line)
		}
	}
}
