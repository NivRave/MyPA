package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-07 part (1): migration 11 drops the legacy `memories` table. A restored backup may still hold
// a row that migration 2 never copied (the baseline fixture does), so migration 11 copies it first,
// exactly as migration 2 did: once, without duplicating a fact already in topic_memories, keeping
// the scope, the timestamps and the embedding. Ported from the P2-4 copy test it replaces.
func TestMigration11_CopiesTheLastMemoriesAndDropsTheTable(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 10)
	ctx := context.Background()

	famX := addHousehold(t, sqlDB, "fam_x")
	for _, u := range []string{"u1", "u2"} {
		insertRow(t, sqlDB, "users", map[string]any{"platform_id": u, "name": u, "role": "family", "family_group": "fam_x", "household_id": famX})
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, m := range []struct {
		user, scope, fact string
		vec               int64
		weight            float64
	}{
		{"u1", "personal", "u1 likes oat milk", 1, 0.9},
		{"u1", "family", "family wifi password is on the fridge", 2, 0.8},
		{"orphan", "personal", "orphan fact", 3, 0.1},
		{"u1", "personal", "already copied", 4, 0.5},
	} {
		_, err := sqlDB.Exec(`INSERT INTO memories (user_id, scope, fact, embedding, created_at) VALUES ($1, $2, $3, $4, $5)`,
			m.user, m.scope, m.fact, mixVec(m.weight, m.vec), created)
		require.NoError(t, err)
	}
	// Migration 2 (or the old remember_fact dual-write) already stored this one in topic_memories.
	insertRow(t, sqlDB, "topic_memories", map[string]any{"user_id": "u1", "household_id": famX, "family_group": "fam_x",
		"topic": models.TopicPersonal, "subtopic": "fact", "scope": "personal", "fact": "already copied", "embedding": mixVec(0.5, 4)})

	migrateTo(t, sqlDB, 11)

	exists, err := tableExists(ctx, sqlDB, "memories")
	require.NoError(t, err)
	assert.False(t, exists, "the legacy table is dropped")
	assertNoFunction(t, sqlDB, "mypa_person_ids_memories")

	gdb := openGorm(t, sqlDB)
	var all []models.TopicMemory
	require.NoError(t, gdb.Order("fact").Find(&all).Error)
	require.Len(t, all, 4, "three facts copied, no duplicate of the one already there")
	byFact := map[string]models.TopicMemory{}
	for _, m := range all {
		byFact[m.Fact] = m
	}
	fam := byFact["family wifi password is on the fridge"]
	assert.Equal(t, "fam_x", fam.FamilyGroup, "the family group comes from the users table")
	assert.Equal(t, famX, fam.HouseholdID, "the household is the person's")
	assert.Equal(t, "family", fam.Scope)
	assert.Equal(t, models.TopicFamily, fam.Topic)
	assert.Equal(t, "u1", fam.CreatedByUserID)
	assert.True(t, fam.CreatedAt.Equal(created), "original timestamp preserved")
	assert.True(t, fam.ValidFrom.Equal(created))
	assert.Len(t, fam.Embedding.Slice(), 3072, "embedding preserved")
	assert.Equal(t, models.TopicPersonal, byFact["u1 likes oat milk"].Topic)
	orphan := byFact["orphan fact"]
	assert.Equal(t, "", orphan.FamilyGroup, "a person without a users row keeps an empty family group")
	assert.Equal(t, models.HoldingHouseholdID, orphan.HouseholdID, "and goes to the holding household, as migration 3 maps one")

	// The copied family memory is found by another member through the v2 search; a personal one
	// stays private.
	c := &Client{DB: gdb}
	got, err := c.SearchSimilarMemories(ctx, scopeAs(t, c, "fam_x", "u2", "family"), mixVec(0.8, 2), 5)
	require.NoError(t, err)
	var facts []string
	for _, m := range got {
		facts = append(facts, m.Fact)
	}
	assert.Contains(t, facts, "family wifi password is on the fridge")
	assert.NotContains(t, facts, "u1 likes oat milk", "u1's personal memory must stay private")
}

// V4-07 part (2): migration 11 folds the legacy workflows into cron automation rules. Each becomes
// a rule of its person's household whose action is the workflow's instruction as a prompt, with
// its name, schedule, state and creation time; the table is dropped.
func TestMigration11_TurnsEachWorkflowIntoACronRule(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 10)
	ctx := context.Background()

	famW := addHousehold(t, sqlDB, "fam_w") // its member is member-fam_w
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, w := range []struct {
		user, name, cron, instruction string
		active                        bool
	}{
		{"member-fam_w", "Morning summary", "0 7 * * *", "Summarise my day", true},
		{"member-fam_w", "Paused", "0 9 * * 1", "Weekly plan", false},
		{"nobody", "Orphan", "30 6 * * *", "Hello", true},
	} {
		_, err := sqlDB.Exec(`INSERT INTO workflows (user_id, name, cron_expression, instruction, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, w.user, w.name, w.cron, w.instruction, w.active, created)
		require.NoError(t, err)
	}

	migrateTo(t, sqlDB, 11)

	exists, err := tableExists(ctx, sqlDB, "workflows")
	require.NoError(t, err)
	assert.False(t, exists, "the legacy table is dropped")
	assertNoFunction(t, sqlDB, "mypa_person_ids_workflows")

	var rules []models.AutomationRule
	require.NoError(t, openGorm(t, sqlDB).Order("id").Find(&rules).Error)
	require.Len(t, rules, 3, "one rule per workflow")
	byName := map[string]models.AutomationRule{}
	for _, r := range rules {
		byName[r.Name] = r
		assert.Equal(t, "cron", r.TriggerType, r.Name)
		assert.Equal(t, "*", r.TriggerTopic, r.Name)
		assert.True(t, r.CreatedAt.Equal(created), "%s keeps its creation time", r.Name)
	}
	morning := byName["Morning summary"]
	assert.Equal(t, "member-fam_w", morning.UserID)
	assert.Equal(t, famW, morning.HouseholdID, "the household of the workflow's person")
	assert.Equal(t, "fam_w", morning.FamilyGroup)
	assert.JSONEq(t, `{"cron": "0 7 * * *"}`, morning.ConditionJSON)
	assert.Equal(t, "prompt: Summarise my day", morning.ActionInstruction)
	assert.True(t, morning.IsActive)
	assert.False(t, byName["Paused"].IsActive, "an inactive workflow stays inactive")
	assert.Equal(t, "prompt: Weekly plan", byName["Paused"].ActionInstruction)
	assert.Equal(t, models.HoldingHouseholdID, byName["Orphan"].HouseholdID, "a workflow without a users row goes to the holding household")
}

// Migration 11 cannot be undone in place (its copies cannot be told apart from later rows): Down
// refuses rather than recreating empty tables. The rollback is the backup taken before the upgrade.
func TestMigration11_DownRefuses(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 11)
	p, err := newMigrationProvider(sqlDB)
	require.NoError(t, err)
	_, err = p.Down(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be undone")
	current, err := p.GetDBVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(11), current, "the schema stays at version 11")
}

func assertNoFunction(t *testing.T, sqlDB *sql.DB, name string) {
	t.Helper()
	var n int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM pg_proc WHERE proname = $1`, name).Scan(&n))
	assert.Zero(t, n, "%s is dropped with its table", name)
}
