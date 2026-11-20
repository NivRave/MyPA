package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// householdColumns returns the tables of the schema that have a household_id column.
func householdColumns(t *testing.T, sqlDB *sql.DB) map[string]bool {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'household_id'`)
	require.NoError(t, err)
	out := map[string]bool{}
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		out[table] = true
	}
	require.NoError(t, rows.Close())
	return out
}

// seedPerson inserts a household, a users row and its person (as Migration B and the user
// creation paths do) and returns the person's ID.
func seedPerson(t *testing.T, sqlDB *sql.DB, householdID, platformID string) string {
	t.Helper()
	var id string
	require.NoError(t, sqlDB.QueryRow(`INSERT INTO users (platform_id, name, role, household_id, family_group, created_at)
		VALUES ($1, 'P', 'family', $2, 'fam', now()) RETURNING id`, platformID, householdID).Scan(&id))
	_, err := sqlDB.Exec(`INSERT INTO people (id, display_name, created_at, updated_at) VALUES ($1, 'P', now(), now())`, id)
	require.NoError(t, err)
	return id
}

func seedHousehold(t *testing.T, sqlDB *sql.DB, label string) string {
	t.Helper()
	var id string
	require.NoError(t, sqlDB.QueryRow(`INSERT INTO households (name, legacy_label, created_at, updated_at)
		VALUES ($1, $1, now(), now()) RETURNING id`, label).Scan(&id))
	return id
}

func companionOf(t *testing.T, sqlDB *sql.DB, c personColumn, rowFilter string, args ...any) sql.NullString {
	t.Helper()
	var v sql.NullString
	require.NoError(t, sqlDB.QueryRow(fmt.Sprintf("SELECT %s::text FROM public.%s WHERE %s", c.Person, c.Table, rowFilter), args...).Scan(&v))
	return v
}

// ADR 0002 Decision 2 (Migration B): person_id beside every person column. A column that names a
// person (its name ends in user_id) without a companion the database fills fails here, so a new
// person column cannot be added without one.
func TestPersonKeys_EveryPersonColumnHasAFilledCompanion(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	require.NoError(t, Migrate(context.Background(), sqlDB))

	rows, err := sqlDB.Query(`SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name LIKE '%user_id' ORDER BY table_name, column_name`)
	require.NoError(t, err)
	var found []string
	for rows.Next() {
		var table, col string
		require.NoError(t, rows.Scan(&table, &col))
		found = append(found, table+"."+col)
	}
	require.NoError(t, rows.Close())
	var listed []string
	for _, c := range personColumns() {
		listed = append(listed, c.Table+"."+c.Legacy)
	}
	sort.Strings(listed)
	assert.Equal(t, found, listed, "every person column in the schema is listed in personColumns, and nothing else")
	require.Len(t, listed, 28, "the 28 person columns of Migration B")

	hh := householdColumns(t, sqlDB)
	h := seedHousehold(t, sqlDB, "fam")
	alice := seedPerson(t, sqlDB, h, "1001")
	bob := seedPerson(t, sqlDB, h, "+15550000002")
	carol := seedPerson(t, sqlDB, h, "1003")

	for _, c := range personColumns() {
		t.Run(c.Table+"."+c.Person, func(t *testing.T) {
			var udt, nullable string
			require.NoError(t, sqlDB.QueryRow(`SELECT udt_name, is_nullable FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, c.Table, c.Person).Scan(&udt, &nullable))
			assert.Equal(t, "uuid", udt)
			assert.Equal(t, "YES", nullable, "an old key that names no person keeps an empty companion")
			var fks int
			require.NoError(t, sqlDB.QueryRow(`
				SELECT count(*) FROM pg_constraint c
				JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
				WHERE c.contype = 'f' AND c.conrelid = ('public.' || $1)::regclass
				  AND c.confrelid = 'public.people'::regclass AND a.attname = $2`, c.Table, c.Person).Scan(&fks))
			assert.Equal(t, 1, fks, "%s.%s references people(id)", c.Table, c.Person)

			// Insert with Alice's platform ID: the database fills Alice's person.
			values := map[string]any{c.Legacy: "1001"}
			if hh[c.Table] {
				values["household_id"] = h
			}
			// The other person column of the table, if any, gets a value too (some are NOT NULL).
			for _, o := range personColumns() {
				if o.Table == c.Table && o.Legacy != c.Legacy {
					values[o.Legacy] = "1003"
				}
			}
			insertRow(t, sqlDB, c.Table, values)
			filter := fmt.Sprintf("%s = $1", c.Legacy)
			assert.Equal(t, alice, companionOf(t, sqlDB, c, filter, "1001").String, "filled on insert")

			// A finance-style key (users.id) maps to the same person.
			_, err := sqlDB.Exec(fmt.Sprintf("UPDATE public.%s SET %s = $1 WHERE %s = '1001'", c.Table, c.Legacy, c.Legacy), alice)
			require.NoError(t, err)
			assert.Equal(t, alice, companionOf(t, sqlDB, c, filter, alice).String, "users.id names the same person")

			// Changing the old key moves the companion with it.
			_, err = sqlDB.Exec(fmt.Sprintf("UPDATE public.%s SET %s = '+15550000002' WHERE %s = $1", c.Table, c.Legacy, c.Legacy), alice)
			require.NoError(t, err)
			assert.Equal(t, bob, companionOf(t, sqlDB, c, filter, "+15550000002").String, "follows the old key on update")

			// Setting the companion explicitly (a merge) is kept.
			_, err = sqlDB.Exec(fmt.Sprintf("UPDATE public.%s SET %s = $1 WHERE %s = '+15550000002'", c.Table, c.Person, c.Legacy), carol)
			require.NoError(t, err)
			assert.Equal(t, carol, companionOf(t, sqlDB, c, filter, "+15550000002").String, "an explicit companion is kept")

			// A write that empties the companion (a save of a struct read before) refills it.
			_, err = sqlDB.Exec(fmt.Sprintf("UPDATE public.%s SET %s = NULL WHERE %s = '+15550000002'", c.Table, c.Person, c.Legacy))
			require.NoError(t, err)
			assert.Equal(t, bob, companionOf(t, sqlDB, c, filter, "+15550000002").String, "an emptied companion is refilled")

			// A key that names nobody stays without a person.
			_, err = sqlDB.Exec(fmt.Sprintf("UPDATE public.%s SET %s = 'dashboard-service' WHERE %s = '+15550000002'", c.Table, c.Legacy, c.Legacy))
			require.NoError(t, err)
			assert.False(t, companionOf(t, sqlDB, c, filter, "dashboard-service").Valid, "dashboard-service is nobody")
		})
	}
}

// ADR 0002 Decision 2 step 2: Migration B makes a person of every user (keeping the ID), one
// channel account by the leading-'+' rule, one membership of the user's household, and fills
// person_id wherever the old value is a known platform ID or users.id; anything else stays empty
// and is counted.
func TestMigrationB_UpgradesEveryUserIntoAPerson(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 3)

	h := seedHousehold(t, sqlDB, "Rave")
	var tg, wa string
	require.NoError(t, sqlDB.QueryRow(`INSERT INTO users (platform_id, name, role, household_id, family_group, created_at)
		VALUES ('2001', 'Tal', 'admin', $1, 'Rave', now()) RETURNING id`, h).Scan(&tg))
	require.NoError(t, sqlDB.QueryRow(`INSERT INTO users (platform_id, name, role, household_id, family_group, created_at)
		VALUES ('+15550002002', 'Noa', 'family', $1, 'Rave', now()) RETURNING id`, h).Scan(&wa))

	hh := householdColumns(t, sqlDB)
	tx := func(table string, values map[string]any) {
		if hh[table] {
			values["household_id"] = h
		}
		insertRow(t, sqlDB, table, values)
	}
	tx("topic_memories", map[string]any{"user_id": "2001", "created_by_user_id": "2001"})
	tx("financial_transactions", map[string]any{"user_id": wa}) // finance wrote users.id
	tx("schedule_events", map[string]any{"user_id": "dashboard-service", "created_by_user_id": "dashboard-service"})
	tx("shared_spaces", map[string]any{"owner_user_id": "+15550002002", "shared_with_user_id": "*"})

	migrateTo(t, sqlDB, 4)

	var people, accounts, memberships int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM people p JOIN users u ON u.id = p.id`).Scan(&people))
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM channel_accounts`).Scan(&accounts))
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM memberships m JOIN users u ON u.id = m.person_id AND u.household_id = m.household_id`).Scan(&memberships))
	assert.Equal(t, 2, people, "every user is a person with the same ID")
	assert.Equal(t, 2, accounts)
	assert.Equal(t, 2, memberships, "each person is a member of their user's household")

	channelOf := func(person string) (ch, ext string) {
		require.NoError(t, sqlDB.QueryRow(`SELECT channel, external_id FROM channel_accounts WHERE person_id = $1`, person).Scan(&ch, &ext))
		return
	}
	ch, ext := channelOf(tg)
	assert.Equal(t, "telegram", ch)
	assert.Equal(t, "2001", ext)
	ch, ext = channelOf(wa)
	assert.Equal(t, "whatsapp", ch)
	assert.Equal(t, "+15550002002", ext)

	var preferredMissing int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM people WHERE preferred_account_id IS NULL`).Scan(&preferredMissing))
	assert.Zero(t, preferredMissing, "each person's one account is their preferred one")

	var roles int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM memberships WHERE role IS NOT NULL`).Scan(&roles))
	assert.Zero(t, roles, "the role stays on users until V4-35 (Migration C)")

	one := func(q string) sql.NullString {
		var v sql.NullString
		require.NoError(t, sqlDB.QueryRow(q).Scan(&v))
		return v
	}
	assert.Equal(t, tg, one(`SELECT person_id::text FROM topic_memories`).String)
	assert.Equal(t, tg, one(`SELECT created_by_person_id::text FROM topic_memories`).String)
	assert.Equal(t, wa, one(`SELECT person_id::text FROM financial_transactions`).String, "users.id maps to its person")
	assert.Equal(t, wa, one(`SELECT owner_person_id::text FROM shared_spaces`).String)
	assert.False(t, one(`SELECT shared_with_person_id::text FROM shared_spaces`).Valid, `"*" is everyone, not a person`)
	assert.False(t, one(`SELECT person_id::text FROM schedule_events`).Valid, "dashboard-service keeps no person")

	unmapped, err := unmappedPersonKeys(context.Background(), sqlDB, personColumns())
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"schedule_events.user_id": 1, "schedule_events.created_by_user_id": 1}, unmapped,
		"the rows without a person are counted; '*' is not a person key")
}
