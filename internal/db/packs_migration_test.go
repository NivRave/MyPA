package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/packs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addHousehold inserts a household with one member at schema version 5 and returns its ID.
func addHousehold(t *testing.T, sqlDB *sql.DB, label string) string {
	t.Helper()
	var id string
	require.NoError(t, sqlDB.QueryRow(`INSERT INTO households (name, legacy_label, created_at, updated_at)
		VALUES ($1, $1, now(), now()) RETURNING id`, label).Scan(&id))
	insertRow(t, sqlDB, "users", map[string]any{"platform_id": "member-" + label, "name": label, "role": "family",
		"family_group": label, "household_id": id})
	return id
}

func packsOf(t *testing.T, sqlDB *sql.DB, householdID string) string {
	t.Helper()
	var p string
	require.NoError(t, sqlDB.QueryRow(`SELECT packs FROM households WHERE id = $1`, householdID).Scan(&p))
	return p
}

// V4-27 done-when: "an upgraded install keeps every feature it was using". On upgrade (migration
// 6, which also runs when a pre-V4 backup is restored, V4-43) every pack that holds data for a
// household is switched on, and only those: a household that used none of them is Core only.
func TestMigrationPacks_EveryPackThatHoldsDataIsSwitchedOn(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 5)

	hh := map[string]string{}
	for _, l := range []string{"Baby", "House", "Finance", "Rules", "Workflow", "GmailLog", "Google", "Nothing", "Several"} {
		hh[l] = addHousehold(t, sqlDB, l)
	}
	row := func(table, label string, values map[string]any) {
		if values == nil {
			values = map[string]any{}
		}
		values["household_id"] = hh[label]
		values["family_group"] = label
		insertRow(t, sqlDB, table, values)
	}
	row("baby_care_logs", "Baby", nil)
	row("vehicle_assets", "House", nil)
	row("recurring_bills", "Finance", nil)
	row("automation_rules", "Rules", nil)
	insertRow(t, sqlDB, "workflows", map[string]any{"user_id": "member-Workflow", "name": "wf", "cron_expression": "0 9 * * *"})
	row("mutation_logs", "GmailLog", map[string]any{"topic": "communication"})
	insertRow(t, sqlDB, "encrypted_credentials", map[string]any{"user_id": "member-Google", "provider": "google_oauth"})
	insertRow(t, sqlDB, "encrypted_credentials", map[string]any{"user_id": "member-Nothing", "provider": "session"})
	row("topic_memories", "Nothing", nil) // Core data switches no pack on
	row("mutation_logs", "Nothing", map[string]any{"topic": "memory"})
	row("baby_profiles", "Several", nil)
	row("baby_inventory_items", "Several", nil)
	row("category_budgets", "Several", map[string]any{"category": "groceries"})
	row("house_contract_records", "Several", nil)
	row("external_api_connectors", "Several", nil)

	migrateTo(t, sqlDB, 6)

	want := map[string]string{
		"Baby": "baby", "House": "house", "Finance": "finance", "Rules": "automations",
		"Workflow": "automations", "GmailLog": "gmail", "Google": "gmail", "Nothing": "",
		"Several": "automations,baby,finance,house",
	}
	for label, p := range want {
		assert.Equal(t, p, packsOf(t, sqlDB, hh[label]), label)
		assert.Equal(t, p, packs.Parse(p).String(), "%s: the stored form is the canonical one", label)
	}
}

// V4-27 and DE-6: a household created after the upgrade starts with Core only, and the packs
// are changed on the scope's own household only.
func TestHouseholdPacks_NewHouseholdStartsCoreOnlyAndOwnHouseholdChanges(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 6)
	c := &Client{DB: openGorm(t, sqlDB)}
	ctx := context.Background()

	mine := &models.Household{Name: "Mine"}
	other := &models.Household{Name: "Other"}
	require.NoError(t, c.CreateHousehold(ctx, mine))
	require.NoError(t, c.CreateHousehold(ctx, other))
	got, err := c.HouseholdPacks(ctx, mine.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.String(), "a new household is Core only")

	s, err := identity.ForStoredRow(ctx, c, mine.ID, "someone")
	require.NoError(t, err)
	h, err := c.SetHouseholdPacks(ctx, s, packs.Parse("finance,baby"))
	require.NoError(t, err)
	assert.Equal(t, "baby,finance", h.Packs)

	got, err = c.HouseholdPacks(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.String(), "another household is untouched")

	_, err = c.SetHouseholdPacks(ctx, identity.Scope{}, packs.Everything())
	assert.ErrorIs(t, err, identity.ErrNoScope)

	got, err = c.HouseholdPacks(ctx, "00000000-0000-0000-0000-00000000abcd")
	require.NoError(t, err)
	assert.Equal(t, "", got.String(), "a household that does not exist has no packs")
}
