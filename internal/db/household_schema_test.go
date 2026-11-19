package db

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// personKeyedTables hold one person's data, keyed by their platform ID, not by household. Each
// needs a reason; a new table must be added here or carry household_id, or the guard fails.
var personKeyedTables = map[string]string{
	"households":             "the tenant itself",
	"audit_sessions":         "one person's conversation log, read by user ID",
	"audit_events":           "events of an audit session",
	"memories":               "legacy per-user memories, copied into topic_memories (P2-4)",
	"scheduled_reminders":    "one person's own reminders",
	"workflows":              "one person's own scheduled macros",
	"encrypted_credentials":  "one person's encrypted Google token",
	"idempotency_records":    "per-user duplicate-execution guard",
	"topic_calendar_configs": "one person's calendar preferences per topic",
	"user_preferences":       "one person's preferences (moves to the person in V4-27)",
}

// ADR 0002 Decision 2: a test fails if a table has a household_id that is nullable, has no
// foreign key, or has no index starting with it — and, so that coverage does not depend on
// someone remembering, if a model is neither household-scoped nor listed as person-keyed.
func TestHouseholdSchema_EveryHouseholdColumnIsGuarded(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	require.NoError(t, Migrate(context.Background(), sqlDB))

	rows, err := sqlDB.Query(`SELECT table_name, is_nullable, udt_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'household_id' ORDER BY table_name`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var table, nullable, udt string
		require.NoError(t, rows.Scan(&table, &nullable, &udt))
		tables = append(tables, table)
		assert.Equal(t, "NO", nullable, "%s.household_id must be NOT NULL", table)
		assert.Equal(t, "uuid", udt, "%s.household_id must be a uuid", table)
	}
	require.NoError(t, rows.Close())
	want := append([]string(nil), labelledTables...)
	sort.Strings(want)
	assert.Equal(t, want, tables, "household_id is on exactly the 20 tables that carried a family label")

	for _, table := range tables {
		var fks int
		require.NoError(t, sqlDB.QueryRow(`
			SELECT count(*) FROM pg_constraint c
			JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
			WHERE c.contype = 'f' AND c.conrelid = ('public.' || $1)::regclass
			  AND c.confrelid = 'public.households'::regclass
			  AND a.attname = 'household_id' AND array_length(c.conkey, 1) = 1`, table).Scan(&fks))
		assert.Equal(t, 1, fks, "%s.household_id must be a foreign key to households(id)", table)

		var leading int
		require.NoError(t, sqlDB.QueryRow(`
			SELECT count(*) FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
			WHERE i.indrelid = ('public.' || $1)::regclass AND a.attname = 'household_id'`, table).Scan(&leading))
		assert.Positive(t, leading, "%s needs an index whose first column is household_id", table)
	}

	// Every model the app migrates is classified.
	all := append([]interface{}{
		&models.Household{}, &models.AuditSession{}, &models.AuditEvent{}, &models.Memory{},
		&models.ScheduledReminder{}, &models.User{}, &models.PairingCode{}, &models.Workflow{},
	}, GetRegisteredModels()...)
	RegisterWave1Models()
	RegisterWave2Models()
	all = append(all, GetRegisteredModels()...)
	scoped := map[string]bool{}
	for _, table := range tables {
		scoped[table] = true
	}
	seen := map[string]bool{}
	for _, m := range all {
		sch, err := schema.Parse(m, &sync.Map{}, schema.NamingStrategy{})
		require.NoError(t, err)
		if seen[sch.Table] {
			continue
		}
		seen[sch.Table] = true
		hasField := sch.LookUpField("HouseholdID") != nil
		_, personKeyed := personKeyedTables[sch.Table]
		switch {
		case hasField:
			assert.True(t, scoped[sch.Table], "model %s has HouseholdID but its table is not household-scoped in the migrations", sch.Table)
			assert.False(t, personKeyed, "%s cannot be both", sch.Table)
		case personKeyed:
		default:
			t.Errorf("model %s has no HouseholdID and is not listed in personKeyedTables: decide which it is", sch.Table)
		}
	}
	for table := range scoped {
		assert.True(t, seen[table], "household table %s has no model", table)
	}
}
