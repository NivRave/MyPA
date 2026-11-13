package db

import (
	"fmt"
	"log/slog"
	"reflect"
	"sync"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)


var (
	registeredModelsMu sync.RWMutex
	registeredModels   []interface{}
)

// RegisterWave1Models explicitly registers all Wave 1 GORM models with the migration system.
func RegisterWave1Models() {
	RegisterModel(
		&models.EncryptedCredential{},
		&models.MutationLog{},
		&models.IdempotencyRecord{},
		&models.TopicMemory{},
		&models.EntityProfile{},
		&models.SharedSpace{},
		&models.SharedTaskReminder{},
		&models.ScheduleEvent{},
		&models.TopicCalendarConfig{},
	)
}

// RegisterWave2Models explicitly registers all Wave 2 GORM models with the migration system.
func RegisterWave2Models() {
	RegisterModel(
		// Track 2E: Baby & Family Care Tracker
		&models.BabyProfile{},
		&models.BabyCareLog{},
		&models.BabyInventoryItem{},
		// Track 2F: House Maintenance, Contracts & Vehicle Vault
		&models.HouseContractRecord{},
		&models.HomeMaintenanceItem{},
		&models.VehicleAsset{},
		// Track 2G: Financial Tracking & Budget Intelligence
		&models.FinancialTransaction{},
		&models.CategoryBudget{},
		&models.RecurringBill{},
		// Track 2H: On-Demand External APIs, Automations & Personal CRM
		&models.ExternalAPIConnector{},
		&models.AutomationRule{},
		&models.UserPreference{},
		&models.PersonalCRMContact{},
	)
}

// RegisterModel registers one or more GORM models to be included in AutoMigrate.
// Models are deduplicated by reflect.TypeOf so multiple registrations do not cause duplicate migrations.
func RegisterModel(models ...interface{}) {
	registeredModelsMu.Lock()
	defer registeredModelsMu.Unlock()

	existingTypes := make(map[reflect.Type]bool)
	for _, m := range registeredModels {
		if m != nil {
			existingTypes[reflect.TypeOf(m)] = true
		}
	}

	for _, m := range models {
		if m == nil {
			continue
		}
		t := reflect.TypeOf(m)
		if !existingTypes[t] {
			existingTypes[t] = true
			registeredModels = append(registeredModels, m)
		}
	}
}

// GetRegisteredModels returns a thread-safe copy of all registered GORM models.
func GetRegisteredModels() []interface{} {
	registeredModelsMu.RLock()
	defer registeredModelsMu.RUnlock()
	res := make([]interface{}, len(registeredModels))
	copy(res, registeredModels)
	return res
}

// ResetRegisteredModels clears all registered models from memory (useful for isolated tests).
func ResetRegisteredModels() {
	registeredModelsMu.Lock()
	defer registeredModelsMu.Unlock()
	registeredModels = nil
}

// ApplyCustomIndexes idempotently applies optimized PostgreSQL indexes:
// 1. Audit session single and composite B-tree indexes.
// 2. High-volume multi-tenant composite indexes (mutation_logs, financial_transactions,
//    baby_care_logs, schedule_events, shared_task_reminders).
// 3. pgvector halfvec(3072) HNSW cosine distance vector indexes on topic_memories and memories.
func ApplyCustomIndexes(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database handle cannot be nil")
	}

	// Only apply PostgreSQL-specific indexes if running on postgres dialector
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil
	}

	// Standard B-tree and composite tenant indexes
	standardIndexes := []string{
		// 1. Audit Sessions
		`CREATE INDEX IF NOT EXISTS idx_audit_sessions_start_time ON audit_sessions (start_time DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_audit_sessions_user_start ON audit_sessions (user_id, start_time DESC);`,

		// 2. Mutation Logs (Tenant History & Rollback Lookups)
		`CREATE INDEX IF NOT EXISTS idx_mutation_logs_family_created ON mutation_logs (family_group, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_mutation_logs_user_created ON mutation_logs (user_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_mutation_logs_family_topic_created ON mutation_logs (family_group, topic, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_mutation_logs_fam_undone_created ON mutation_logs (family_group, is_undone, created_at DESC);`,

		// 3. Financial Transactions (Category Filtering & Monthly Summaries)
		`CREATE INDEX IF NOT EXISTS idx_financial_transactions_family_date ON financial_transactions (family_group, transaction_date DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_financial_transactions_user_date ON financial_transactions (user_id, transaction_date DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_financial_transactions_fam_cat ON financial_transactions (family_group, category);`,
		`CREATE INDEX IF NOT EXISTS idx_financial_transactions_fam_type_cat_date ON financial_transactions (family_group, type, category, transaction_date);`,

		// 4. Baby Care Logs (Medication Cooldowns & Trend Aggregation)
		`CREATE INDEX IF NOT EXISTS idx_baby_care_logs_family_timestamp ON baby_care_logs (family_group, timestamp DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_baby_care_logs_baby_timestamp ON baby_care_logs (baby_id, timestamp DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_baby_care_logs_lookup ON baby_care_logs (family_group, baby_id, log_type, timestamp DESC);`,

		// 5. Schedule Events (Upcoming Agenda Lookups)
		`CREATE INDEX IF NOT EXISTS idx_schedule_events_family_start ON schedule_events (family_group, start_time ASC);`,
		`CREATE INDEX IF NOT EXISTS idx_schedule_events_user_start ON schedule_events (user_id, start_time ASC);`,
		`CREATE INDEX IF NOT EXISTS idx_schedule_events_fam_topic_start ON schedule_events (family_group, topic, start_time ASC);`,

		// 6. Shared Task Reminders (Due Reminders Polling & Active Tasks)
		`CREATE INDEX IF NOT EXISTS idx_shared_task_reminders_due_completed ON shared_task_reminders (due_time, is_completed);`,
		`CREATE INDEX IF NOT EXISTS idx_shared_task_reminders_family_completed_due ON shared_task_reminders (family_group, is_completed, due_time ASC);`,

		// 7. House Contracts (Lease / Warranty Expirations)
		`CREATE INDEX IF NOT EXISTS idx_house_contracts_fam_exp ON house_contract_records (family_group, expiration_or_renewal_date);`,

		// 8. Topic Memories (Tenant Memory Retrieval)
		`CREATE INDEX IF NOT EXISTS idx_topic_memories_family_created ON topic_memories (family_group, created_at DESC);`,
	}

	for _, stmt := range standardIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("failed executing index statement %q: %w", stmt, err)
		}
	}

	// pgvector HNSW vector indexes (halfvec(3072) required for dimensions > 2000)
	vectorIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_topic_memories_embedding ON topic_memories USING hnsw ((embedding::halfvec(3072)) halfvec_cosine_ops);`,
		`CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories USING hnsw ((embedding::halfvec(3072)) halfvec_cosine_ops);`,
	}

	for _, stmt := range vectorIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			// Log warning on failure to support environments with older pgvector or constrained memory
			slog.Warn("could not create halfvec hnsw vector index (check pgvector >= 0.7.0)", "statement", stmt, "error", err)
		}
	}

	return nil
}

