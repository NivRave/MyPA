package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The 20 tables that carried a family label before V4-30 (ADR 0002 Decision 2, Migration A).
var labelledTables = []string{
	"automation_rules", "baby_care_logs", "baby_inventory_items", "baby_profiles", "category_budgets",
	"entity_profiles", "external_api_connectors", "financial_transactions", "home_maintenance_items",
	"house_contract_records", "mutation_logs", "pairing_codes", "personal_crm_contacts", "recurring_bills",
	"schedule_events", "shared_spaces", "shared_task_reminders", "topic_memories", "users", "vehicle_assets",
}

// ownedTables are the data tables whose reads before V4-30 also matched the person (user_id),
// whatever the label: an empty-label row there was visible to its owner (DP-26).
var ownedTables = map[string]bool{
	"automation_rules": true, "entity_profiles": true, "external_api_connectors": true, "financial_transactions": true,
	"mutation_logs": true, "personal_crm_contacts": true, "schedule_events": true, "topic_memories": true,
}

// migrateTo brings an empty database to the given schema version.
func migrateTo(t *testing.T, sqlDB *sql.DB, version int64) {
	t.Helper()
	p, err := newMigrationProvider(sqlDB)
	require.NoError(t, err)
	_, err = p.UpTo(context.Background(), version)
	require.NoError(t, err)
}

// insertRow inserts one row into table with the given column values, filling every other
// NOT NULL column that has no default with a value of its type.
func insertRow(t *testing.T, sqlDB *sql.DB, table string, values map[string]any) {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT column_name, udt_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND is_nullable = 'NO' AND column_default IS NULL`, table)
	require.NoError(t, err)
	cols, args, exprs := []string{}, []any{}, []string{}
	for rows.Next() {
		var col, udt string
		require.NoError(t, rows.Scan(&col, &udt))
		if _, given := values[col]; given {
			continue
		}
		cols = append(cols, col)
		switch udt {
		case "uuid":
			exprs = append(exprs, "gen_random_uuid()")
		case "vector":
			exprs = append(exprs, "array_fill(0, ARRAY[3072])::vector")
		case "bool":
			exprs = append(exprs, "false")
		case "timestamptz", "timestamp":
			exprs = append(exprs, "now()")
		case "jsonb", "json":
			exprs = append(exprs, "'{}'")
		case "int2", "int4", "int8", "numeric", "float4", "float8":
			exprs = append(exprs, "0")
		default:
			exprs = append(exprs, "md5(random()::text)")
		}
	}
	require.NoError(t, rows.Close())
	for col, v := range values {
		cols = append(cols, col)
		args = append(args, v)
		exprs = append(exprs, fmt.Sprintf("$%d", len(args)))
	}
	stmt := fmt.Sprintf("INSERT INTO public.%s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(exprs, ", "))
	_, err = sqlDB.Exec(stmt, args...)
	require.NoError(t, err, stmt)
}

func familyGroupNullable(t *testing.T, sqlDB *sql.DB, table string) bool {
	t.Helper()
	var nullable string
	require.NoError(t, sqlDB.QueryRow(`SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'family_group'`, table).Scan(&nullable))
	return nullable == "YES"
}

// ADR 0002 Decision 2, Migration A, on a database at the v3.4.0 schema holding every kind of
// label: one household per label (trimmed, otherwise exact, so case variants stay apart), a
// household of their own for users without a label (keyed by platform ID, which their rows
// carry), a memberless household for a label found only on data rows, an empty-label row its
// owner could read in the owner's household (DP-26), and the memberless holding household for
// every other row without a label. Every row of the 20 tables gets exactly the household its
// label (or owner) maps to, and the row counts per label equal the row counts per household.
func TestMigrationA_EveryLabelBecomesItsHousehold(t *testing.T) {
	dsn := newEmptyTestDatabase(t)
	sqlDB := openRaw(t, dsn)
	migrateTo(t, sqlDB, 2) // the v3.4.0 schema

	// Users: "Levi" twice (one padded), a case variant, two users without a label, and a user
	// whose label is an unlabelled user's platform ID (the same tenant before V4-30).
	users := []struct{ id, name, label string }{
		{"1001", "Dana", "Levi"},
		{"1002", "Omer", " Levi "},
		{"1003", "Noa", "levi"},
		{"4004", "Gil", ""},
		{"5005", "Tal", ""},
		{"5006", "Ido", "5005"},
	}
	for _, u := range users {
		insertRow(t, sqlDB, "users", map[string]any{"platform_id": u.id, "name": u.name, "role": "family", "family_group": u.label})
	}
	_, err := sqlDB.Exec(`UPDATE users SET family_group = NULL WHERE platform_id = '5005'`)
	require.NoError(t, err)
	var danaUUID string
	require.NoError(t, sqlDB.QueryRow(`SELECT id FROM users WHERE platform_id = '1001'`).Scan(&danaUUID))
	for _, l := range []string{"Levi", "PairOnly", ""} {
		insertRow(t, sqlDB, "pairing_codes", map[string]any{"code": "c-" + l, "family_group": l, "expires_at": "2999-01-01T00:00:00Z"})
	}

	// Data rows: every data table gets one row per kind of label.
	dataLabels := []string{"Levi", " Levi", "levi", "4004", "5005", "DataOnly", ""}
	for _, table := range labelledTables {
		if table == "users" || table == "pairing_codes" {
			continue
		}
		for i, l := range dataLabels {
			values := map[string]any{"family_group": l}
			if table == "category_budgets" {
				values["category"] = fmt.Sprintf("cat-%d", i) // unique per (family_group, category)
			}
			insertRow(t, sqlDB, table, values)
		}
		if familyGroupNullable(t, sqlDB, table) {
			values := map[string]any{"family_group": nil}
			if table == "category_budgets" {
				values["category"] = "cat-null"
			}
			insertRow(t, sqlDB, table, values)
		}
		if ownedTables[table] {
			// Empty-label rows with an owner: an unlabelled user by platform ID, a labelled user
			// by users.id (finance writes that), and an owner who is nobody.
			insertRow(t, sqlDB, table, map[string]any{"family_group": "", "user_id": "4004"})
			insertRow(t, sqlDB, table, map[string]any{"family_group": "", "user_id": danaUUID})
			insertRow(t, sqlDB, table, map[string]any{"family_group": "", "user_id": "no-such-user"})
		}
	}

	// Rows per (table, label) before the migration.
	before := map[string]map[string]int{}
	for _, table := range labelledTables {
		before[table] = map[string]int{}
		rows, err := sqlDB.Query(fmt.Sprintf(`SELECT COALESCE(family_group, '<null>'), count(*) FROM public.%s GROUP BY 1`, table))
		require.NoError(t, err)
		for rows.Next() {
			var l string
			var n int
			require.NoError(t, rows.Scan(&l, &n))
			before[table][l] = n
		}
		require.NoError(t, rows.Close())
	}

	require.NoError(t, Migrate(context.Background(), sqlDB))

	// The households the migration made, by label.
	byLabel := map[string]string{}
	rows, err := sqlDB.Query(`SELECT id, COALESCE(legacy_label, '<holding>'), name FROM households`)
	require.NoError(t, err)
	names := map[string]string{}
	for rows.Next() {
		var id, label, name string
		require.NoError(t, rows.Scan(&id, &label, &name))
		require.NotContains(t, byLabel, label, "one household per label")
		byLabel[label] = id
		names[label] = name
	}
	require.NoError(t, rows.Close())
	assert.ElementsMatch(t, []string{"Levi", "levi", "4004", "5005", "PairOnly", "DataOnly", "<holding>"}, keys(byLabel),
		"case variants stay apart; padded labels are trimmed; unlabelled users are keyed by platform ID")
	assert.Equal(t, models.HoldingHouseholdID, byLabel["<holding>"], "the holding household has the fixed ID")
	assert.Equal(t, "Gil", names["4004"], "an unlabelled user's household is named after them")

	// Where each pre-V4 row must land: by its label, else by its owner where the owner could read
	// it, else in the holding household.
	ownerHousehold := map[string]string{"4004": byLabel["4004"], danaUUID: byLabel["Levi"]}
	expected := func(table, label, owner string) string {
		l := strings.TrimSpace(label)
		if label != "<null>" && l != "" {
			return byLabel[l]
		}
		if h, ok := ownerHousehold[owner]; ok && ownedTables[table] {
			return h
		}
		return byLabel["<holding>"]
	}
	userHousehold := map[string]string{"1001": "Levi", "1002": "Levi", "1003": "levi", "4004": "4004", "5005": "5005", "5006": "5005"}

	for _, table := range labelledTables {
		var nulls int
		require.NoError(t, sqlDB.QueryRow(fmt.Sprintf(`SELECT count(*) FROM public.%s WHERE household_id IS NULL`, table)).Scan(&nulls))
		assert.Zero(t, nulls, "%s: no row without a household", table)

		if table == "users" {
			for pid, label := range userHousehold {
				var got string
				require.NoError(t, sqlDB.QueryRow(`SELECT household_id FROM users WHERE platform_id = $1`, pid).Scan(&got))
				assert.Equal(t, byLabel[label], got, "user %s", pid)
			}
			continue
		}
		ownerCol := "''"
		if ownedTables[table] {
			ownerCol = "COALESCE(user_id, '')"
		}
		perHousehold := map[string]int{}
		wantPerHousehold := map[string]int{}
		perLabel := map[string]int{}
		rows, err := sqlDB.Query(fmt.Sprintf(`SELECT COALESCE(family_group, '<null>'), %s, household_id FROM public.%s`, ownerCol, table))
		require.NoError(t, err)
		for rows.Next() {
			var l, owner, h string
			require.NoError(t, rows.Scan(&l, &owner, &h))
			assert.Equal(t, expected(table, l, owner), h, "%s: a row labelled %q owned by %q", table, l, owner)
			perHousehold[h]++
			wantPerHousehold[expected(table, l, owner)]++
			perLabel[l]++
		}
		require.NoError(t, rows.Close())
		assert.Equal(t, before[table], perLabel, "%s: the migration moves no row and changes no label", table)
		assert.Equal(t, wantPerHousehold, perHousehold, "%s: rows per label equal rows per household", table)
	}

	// The old label is still there, unchanged, for the comparison report of V4-44.
	var label string
	require.NoError(t, sqlDB.QueryRow(`SELECT family_group FROM users WHERE platform_id = '1002'`).Scan(&label))
	assert.Equal(t, " Levi ", label)

	// Nobody can join the holding household: its pairing code is no longer valid.
	c, err := Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.GetPairingCode("c-")
	assert.Error(t, err, "a pairing code without a family is refused")
	pc, err := c.GetPairingCode("c-PairOnly")
	require.NoError(t, err)
	assert.Equal(t, byLabel["PairOnly"], pc.HouseholdID)
}

// A database with no rows gets no household at all, not even the holding one.
func TestMigrationA_EmptyDatabaseGetsNoHousehold(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	require.NoError(t, Migrate(context.Background(), sqlDB))
	var n int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM households`).Scan(&n))
	assert.Zero(t, n)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
