package db

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryScopeMigration is the migration that corrects topic_memories.scope (BL-26).
const memoryScopeMigration = 13

// BL-26, on the committed v3.4.0 baseline plus added rows: migration 13 corrects every stored
// scope by the rule save_topic_memory now applies. A value that is "family" after trimming and
// lower-casing becomes family; anything else that is not already one of the two literals becomes
// personal; the literals are left alone. It logs how many rows of each kind it changed, never
// their content.
func TestMigration13_NormalisesMemoryScopes(t *testing.T) {
	dsn := newEmptyTestDatabase(t)
	fixture := filepath.Join("..", "..", "cmd", "mypa", "testdata", "baseline_v3.4.0.dump")
	require.NoError(t, RestoreDatabase(dsn, fixture, RestoreOptions{FreshSchema: true, Run: pgtest.ToolRunner()}))
	sqlDB := openRaw(t, dsn)
	migrateTo(t, sqlDB, memoryScopeMigration-1)

	var baseline []struct {
		ID    int64
		Scope string
	}
	gdb := openGorm(t, sqlDB)
	require.NoError(t, gdb.Raw(`SELECT id, scope FROM topic_memories ORDER BY id`).Scan(&baseline).Error)
	require.NotEmpty(t, baseline, "the baseline holds memories")

	var household string
	require.NoError(t, sqlDB.QueryRow(`SELECT household_id FROM users ORDER BY platform_id LIMIT 1`).Scan(&household))
	added := map[string]string{ // fact -> scope as stored before the migration
		"fact Family":     "Family",
		"fact FAMILY pad": "  FAMILY ",
		"fact tab":        "\tFamily\n",
		"fact nbsp":       "\u00a0family",
		"fact family":     "family",
		"fact personal":   "personal",
		"fact Personal":   "Personal",
		"fact shared":     "shared",
		"fact empty":      "",
	}
	for fact, scope := range added {
		insertRow(t, sqlDB, "topic_memories", map[string]any{"user_id": "1001", "household_id": household,
			"family_group": "", "topic": "personal", "subtopic": "", "scope": scope, "fact": fact})
	}

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	migrateTo(t, sqlDB, memoryScopeMigration)
	slog.SetDefault(prev)

	want := map[string]string{
		"fact Family": "family", "fact FAMILY pad": "family", "fact family": "family", "fact tab": "family", "fact nbsp": "family",
		"fact personal": "personal", "fact Personal": "personal", "fact shared": "personal", "fact empty": "personal",
	}
	for fact, scope := range want {
		var got string
		require.NoError(t, sqlDB.QueryRow(`SELECT scope FROM topic_memories WHERE fact = $1`, fact).Scan(&got))
		assert.Equal(t, scope, got, "scope of %q (stored as %q)", fact, added[fact])
	}
	for _, row := range baseline {
		var got string
		require.NoError(t, sqlDB.QueryRow(`SELECT scope FROM topic_memories WHERE id = $1`, row.ID).Scan(&got))
		assert.Equal(t, row.Scope, got, "a baseline row that already held a literal is unchanged")
	}

	out := logs.String()
	assert.Contains(t, out, "to_family=4", "four rows became family, whatever white space surrounded them")
	assert.Contains(t, out, "to_personal=3", "three rows became personal")
	for fact := range added {
		assert.NotContains(t, out, fact, "the log carries no memory content")
	}
	assert.NotContains(t, out, "FAMILY", "the log carries no stored value")
	assert.NotContains(t, out, "shared")

	var leftover int64
	require.NoError(t, gdb.Raw(`SELECT count(*) FROM topic_memories WHERE scope NOT IN ('family', 'personal')`).Scan(&leftover).Error)
	assert.Zero(t, leftover, "no row is left with another scope")
}
