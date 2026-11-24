package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"
)

// Person keys (V4-32, ADR 0002 Decision 2, Migration B). Every column that names a person by the
// key the code wrote before V4-32 (a platform ID, or users.id in finance) gets a companion column
// holding people.id: user_id beside person_id, created_by_user_id beside created_by_person_id,
// and so on. The old column is still written, by the code, until V4-44 drops it; the new one is
// filled by the database, so no write path can forget it:
//
//   - mypa_person_of(key) maps an old key to its person: the person whose users row has that
//     platform ID, the person with that ID, or the one person owning a channel account with that
//     external ID. Anything else (for example "dashboard-service" or "*") maps to nothing.
//   - A trigger per table fills each companion on insert when it is empty, and on update when it
//     is emptied or the old key changes without the companion changing. Setting the companion
//     explicitly (a merge moves rows to the surviving person) is kept.
//
// Person-level reads filter the companion (personFilter).

//go:embed people_migration.sql
var peopleMigrationSQL string

// personColumn is one old person column and its companion.
type personColumn struct {
	Table  string
	Legacy string
	Person string
}

// migration4PersonColumns are the 28 person columns that existed when migration 4 was written.
// Frozen with migration 4: a new person column needs its own migration (and an entry in
// personColumns), and TestPersonKeys_EveryPersonColumnHasAFilledCompanion fails until it has one.
var migration4PersonColumns = []personColumn{
	{"audit_sessions", "user_id", "person_id"},
	{"automation_rules", "user_id", "person_id"},
	{"baby_care_logs", "logged_by_user_id", "logged_by_person_id"},
	{"baby_profiles", "updated_by_user_id", "updated_by_person_id"},
	{"encrypted_credentials", "user_id", "person_id"},
	{"entity_profiles", "user_id", "person_id"},
	{"entity_profiles", "updated_by_user_id", "updated_by_person_id"},
	{"external_api_connectors", "user_id", "person_id"},
	{"financial_transactions", "user_id", "person_id"},
	{"home_maintenance_items", "updated_by_user_id", "updated_by_person_id"},
	{"house_contract_records", "created_by_user_id", "created_by_person_id"},
	{"idempotency_records", "user_id", "person_id"},
	{"memories", "user_id", "person_id"},
	{"mutation_logs", "user_id", "person_id"},
	{"personal_crm_contacts", "user_id", "person_id"},
	{"schedule_events", "user_id", "person_id"},
	{"schedule_events", "created_by_user_id", "created_by_person_id"},
	{"scheduled_reminders", "user_id", "person_id"},
	{"shared_spaces", "owner_user_id", "owner_person_id"},
	{"shared_spaces", "shared_with_user_id", "shared_with_person_id"},
	{"shared_task_reminders", "created_by_user_id", "created_by_person_id"},
	{"shared_task_reminders", "assigned_to_user_id", "assigned_to_person_id"},
	{"shared_task_reminders", "completed_by_user_id", "completed_by_person_id"},
	{"topic_calendar_configs", "user_id", "person_id"},
	{"topic_memories", "user_id", "person_id"},
	{"topic_memories", "created_by_user_id", "created_by_person_id"},
	{"user_preferences", "user_id", "person_id"},
	{"workflows", "user_id", "person_id"},
}

// droppedTables are tables of migration4PersonColumns that a later migration dropped: memories
// and workflows, by migration 11 (V4-07).
var droppedTables = map[string]bool{"memories": true, "workflows": true}

// personColumns is every person column today.
func personColumns() []personColumn {
	var out []personColumn
	for _, c := range migration4PersonColumns {
		if !droppedTables[c.Table] {
			out = append(out, c)
		}
	}
	return out
}

// personOfFunctionSQL maps an old person key to people.id (see the package comment above).
const personOfFunctionSQL = `CREATE OR REPLACE FUNCTION public.mypa_person_of(key text) RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT CASE WHEN COALESCE(btrim(key), '') = '' THEN NULL ELSE COALESCE(
    (SELECT p.id FROM public.people p JOIN public.users u ON u.id = p.id
      WHERE btrim(u.platform_id) = btrim(key) LIMIT 1),
    (SELECT p.id FROM public.people p WHERE p.id::text = btrim(key) LIMIT 1),
    (SELECT CASE WHEN count(DISTINCT ca.person_id) = 1 THEN min(ca.person_id::text)::uuid END
      FROM public.channel_accounts ca WHERE ca.external_id = btrim(key))
  ) END
$$`

// tablesOf groups columns by table, in first-seen order.
func tablesOf(cols []personColumn) ([]string, map[string][]personColumn) {
	var order []string
	by := map[string][]personColumn{}
	for _, c := range cols {
		if _, ok := by[c.Table]; !ok {
			order = append(order, c.Table)
		}
		by[c.Table] = append(by[c.Table], c)
	}
	return order, by
}

// personTriggerSQL returns, per table, the statements that (re)create its fill trigger.
func personTriggerSQL(cols []personColumn) []string {
	order, by := tablesOf(cols)
	var out []string
	for _, table := range order {
		var ins, upd strings.Builder
		for _, c := range by[table] {
			fmt.Fprintf(&ins, "    IF NEW.%[1]s IS NULL THEN NEW.%[1]s := public.mypa_person_of(NEW.%[2]s); END IF;\n", c.Person, c.Legacy)
			fmt.Fprintf(&upd, "    IF NEW.%[1]s IS NULL OR (NEW.%[2]s IS DISTINCT FROM OLD.%[2]s AND NEW.%[1]s IS NOT DISTINCT FROM OLD.%[1]s) THEN\n"+
				"      NEW.%[1]s := public.mypa_person_of(NEW.%[2]s);\n    END IF;\n", c.Person, c.Legacy)
		}
		fn := "mypa_person_ids_" + table
		out = append(out,
			fmt.Sprintf("CREATE OR REPLACE FUNCTION public.%s() RETURNS trigger LANGUAGE plpgsql AS $$\nBEGIN\n  IF TG_OP = 'INSERT' THEN\n%s  ELSE\n%s  END IF;\n  RETURN NEW;\nEND\n$$", fn, ins.String(), upd.String()),
			fmt.Sprintf("DROP TRIGGER IF EXISTS mypa_person_ids ON public.%s", table),
			fmt.Sprintf("CREATE TRIGGER mypa_person_ids BEFORE INSERT OR UPDATE ON public.%s FOR EACH ROW EXECUTE FUNCTION public.%s()", table, fn),
		)
	}
	return out
}

// personForeignKeySQL adds, if missing, the foreign key of each companion: a deleted person
// leaves the row with only its old key, as before V4-32.
func personForeignKeySQL(cols []personColumn) []string {
	var out []string
	for _, c := range cols {
		name := fmt.Sprintf("fk_%s_%s", c.Table, c.Person)
		out = append(out, fmt.Sprintf(`DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = '%[1]s') THEN
    ALTER TABLE public.%[2]s ADD CONSTRAINT %[1]s FOREIGN KEY (%[3]s) REFERENCES public.people(id) ON DELETE SET NULL;
  END IF;
END $$`, name, c.Table, c.Person))
	}
	return out
}

// migrationB is Go migration 4: the identity tables and their data (people_migration.sql), then
// the companion columns, filled, with their foreign keys, indexes and triggers. It returns how
// many old keys found no person, per column, for the log.
func migrationB(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	if _, err := tx.ExecContext(ctx, peopleMigrationSQL); err != nil {
		return nil, fmt.Errorf("identity tables: %w", err)
	}
	if _, err := tx.ExecContext(ctx, personOfFunctionSQL); err != nil {
		return nil, fmt.Errorf("mypa_person_of: %w", err)
	}
	cols := migration4PersonColumns
	for _, c := range cols {
		stmts := []string{
			fmt.Sprintf("ALTER TABLE public.%s ADD COLUMN %s uuid", c.Table, c.Person),
			fmt.Sprintf("UPDATE public.%[1]s SET %[2]s = public.mypa_person_of(%[3]s) WHERE COALESCE(btrim(%[3]s), '') <> ''", c.Table, c.Person, c.Legacy),
			fmt.Sprintf("CREATE INDEX idx_%[1]s_%[2]s ON public.%[1]s USING btree (%[2]s)", c.Table, c.Person),
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", c.Table, c.Person, err)
			}
		}
	}
	for _, s := range append(personForeignKeySQL(cols), personTriggerSQL(cols)...) {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return nil, fmt.Errorf("person keys: %w", err)
		}
	}
	return unmappedPersonKeys(ctx, tx, cols)
}

// unmappedPersonKeys counts, per column, the rows whose old key names no person ("*" and empty
// values are not keys and are not counted).
func unmappedPersonKeys(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, cols []personColumn) (map[string]int64, error) {
	out := map[string]int64{}
	for _, c := range cols {
		var n int64
		err := q.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT count(*) FROM public.%[1]s WHERE %[2]s IS NULL AND COALESCE(btrim(%[3]s), '') NOT IN ('', '*')",
			c.Table, c.Person, c.Legacy)).Scan(&n)
		if err != nil {
			return nil, fmt.Errorf("count %s.%s: %w", c.Table, c.Legacy, err)
		}
		if n > 0 {
			out[c.Table+"."+c.Legacy] = n
		}
	}
	return out, nil
}

// logUnmappedPersonKeys is migration 4's report (ADR 0002 Decision 2 step 2: the migration prints
// how many values kept no person). Counts only, never the keys themselves (principle 4).
func logUnmappedPersonKeys(unmapped map[string]int64) {
	if len(unmapped) == 0 {
		slog.Info("migration B: every person key maps to a person")
		return
	}
	var total int64
	for col, n := range unmapped {
		total += n
		slog.Info("migration B: rows whose person key names no person keep only the old key", "column", col, "rows", n)
	}
	slog.Info("migration B: rows without a person in total", "rows", total)
}

// applyPersonKeys installs the person-key function, foreign keys and triggers on a schema built
// by AutoMigrate (tests and local experiments), so it behaves like a migrated one.
func applyPersonKeys(db *gorm.DB) error {
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	stmts := []string{personOfFunctionSQL}
	stmts = append(stmts, identityForeignKeySQL...)
	stmts = append(stmts, personForeignKeySQL(personColumns())...)
	stmts = append(stmts, personTriggerSQL(personColumns())...)
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			return fmt.Errorf("person keys: %w", err)
		}
	}
	return nil
}

// identityForeignKeySQL are the identity tables' constraints (people_migration.sql, and Migration
// C's for sessions), for a schema built by AutoMigrate.
var identityForeignKeySQL = func() []string {
	type fk struct{ name, table, def string }
	fks := []fk{
		{"fk_channel_accounts_person", "channel_accounts", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE"},
		{"fk_people_preferred_account", "people", "FOREIGN KEY (preferred_account_id) REFERENCES public.channel_accounts(id) ON DELETE SET NULL"},
		{"fk_memberships_person", "memberships", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE"},
		{"fk_memberships_household", "memberships", "FOREIGN KEY (household_id) REFERENCES public.households(id)"},
		{"chk_memberships_role", "memberships", "CHECK (role IS NULL OR role IN ('owner', 'adult', 'child', 'guest'))"},
		{"fk_access_tokens_household", "access_tokens", "FOREIGN KEY (household_id) REFERENCES public.households(id)"},
		{"fk_access_tokens_person", "access_tokens", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE"},
		{"fk_access_tokens_created_by", "access_tokens", "FOREIGN KEY (created_by) REFERENCES public.people(id) ON DELETE SET NULL"},
		{"fk_access_tokens_issued_on", "access_tokens", "FOREIGN KEY (issued_on_account_id) REFERENCES public.channel_accounts(id) ON DELETE CASCADE"},
		{"chk_access_tokens_purpose", "access_tokens", "CHECK (purpose IN ('invite', 'link', 'signin'))"},
		// Migration C (00005_roles_sessions.sql, V4-35).
		{"fk_sessions_person", "sessions", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE"},
		// 00008_proactive_deliveries.sql (V4-33).
		{"fk_proactive_deliveries_person", "proactive_deliveries", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE CASCADE"},
		// 00010_model_usage.sql (V4-04): a removed person's calls stay in the install's total.
		{"fk_model_usage_person", "model_usage", "FOREIGN KEY (person_id) REFERENCES public.people(id) ON DELETE SET NULL"},
	}
	out := make([]string, 0, len(fks))
	for _, f := range fks {
		out = append(out, fmt.Sprintf(`DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = '%[1]s') THEN
    ALTER TABLE public.%[2]s ADD CONSTRAINT %[1]s %[3]s;
  END IF;
END $$`, f.name, f.table, f.def))
	}
	return out
}()

// personIs is the person-level read of V4-32 for one column pair: the rows of the person behind
// key (a person key as the modules write it: a platform ID, or users.id), by the companion, plus
// rows that name no person and carry exactly that old key (as before V4-32). The person is
// resolved once per query. Every account of one person writes the same key, and a merge moves
// rows by the companion, so this reads the same data whichever channel the person writes from.
func personIs(legacy, person, key string) (string, []interface{}) {
	return fmt.Sprintf("(%[2]s = (SELECT public.mypa_person_of(?)) OR (%[2]s IS NULL AND %[1]s = ?))", legacy, person),
		[]interface{}{key, key}
}
