package db

import (
	"fmt"
	"github.com/nivik/mypa/internal/models"
	"log/slog"

	"gorm.io/gorm"
)

// legacyMemoriesSQL is goose migration 2 (production). It runs on the schema of version 2,
// before migration 3 adds household_id, so it must not name that column; migration 3 then maps
// the copied rows to households like every other row. Frozen: a migration never changes.
const legacyMemoriesSQL = `
		INSERT INTO topic_memories
			(user_id, family_group, topic, subtopic, tags_json, fact, scope,
			 created_by_user_id, valid_from, is_pinned, embedding, created_at)
		SELECT
			m.user_id,
			COALESCE(u.family_group, ''),
			CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
			'fact',
			'[]'::jsonb,
			m.fact,
			CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
			m.user_id,
			m.created_at,
			false,
			m.embedding,
			m.created_at
		FROM memories m
		LEFT JOIN users u ON u.platform_id = m.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM topic_memories t
			WHERE t.user_id = m.user_id AND t.fact = m.fact
		)`

// legacyMemoriesHouseholdSQL is the same copy for the AutoMigrate path (tests and local
// experiments), whose schema already has household_id (V4-30): a row takes its user's household,
// and a row whose user is unknown goes to the holding household, as migration 3 maps it.
const legacyMemoriesHouseholdSQL = `
		INSERT INTO topic_memories
			(user_id, family_group, household_id, topic, subtopic, tags_json, fact, scope,
			 created_by_user_id, valid_from, is_pinned, embedding, created_at)
		SELECT
			m.user_id,
			COALESCE(u.family_group, ''),
			COALESCE(u.household_id, '` + holdingHouseholdSQLID + `'::uuid),
			CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
			'fact',
			'[]'::jsonb,
			m.fact,
			CASE WHEN m.scope = 'family' THEN 'family' ELSE 'personal' END,
			m.user_id,
			m.created_at,
			false,
			m.embedding,
			m.created_at
		FROM memories m
		LEFT JOIN users u ON u.platform_id = m.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM topic_memories t
			WHERE t.user_id = m.user_id AND t.fact = m.fact
		)`

const holdingHouseholdSQLID = models.HoldingHouseholdID

// legacyOrphansHoldingSQL creates the holding household when a legacy row has no known user.
const legacyOrphansHoldingSQL = `
		INSERT INTO households (id, name, legacy_label, created_at, updated_at)
		SELECT '` + holdingHouseholdSQLID + `', 'Rows without a family (pre-V4)', NULL, now(), now()
		WHERE EXISTS (
			SELECT 1 FROM memories m
			LEFT JOIN users u ON u.platform_id = m.user_id
			WHERE u.household_id IS NULL
			  AND NOT EXISTS (SELECT 1 FROM topic_memories t WHERE t.user_id = m.user_id AND t.fact = m.fact)
		)
		ON CONFLICT (id) DO NOTHING`

// MigrateLegacyMemories copies v1 `memories` rows into v2 `topic_memories` (P2-4).
//
//   - Idempotent: a fact already present in topic_memories for the same user (e.g. stored by the
//     old remember_fact dual-write) is skipped, so this is safe to run on every startup.
//   - family_group comes from the users table (v1 rows had none); unknown users get ”.
//   - scope 'family' → topic 'family', otherwise 'personal'; subtopic 'fact'.
//   - Embeddings and original timestamps are preserved, so no re-embedding is needed.
//
// The legacy table is intentionally kept (read-only, no code writes to it any more) until the
// migration has been verified in production; dropping it is a separate, manual step.
func MigrateLegacyMemories(gdb *gorm.DB) (int64, error) {
	if err := gdb.Exec(legacyOrphansHoldingSQL).Error; err != nil {
		return 0, fmt.Errorf("migrate legacy memories: holding household: %w", err)
	}
	res := gdb.Exec(legacyMemoriesHouseholdSQL)
	if res.Error != nil {
		return 0, fmt.Errorf("migrate legacy memories: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		slog.Info("migrated legacy memories into topic_memories", "rows", res.RowsAffected)
	}
	return res.RowsAffected, nil
}
