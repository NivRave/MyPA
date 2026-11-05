package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableNameForEntity_RejectsSystemTables(t *testing.T) {
	systemEntities := []string{
		"users",
		"pairing_codes",
		"encrypted_credentials",
		"audit_sessions",
		"audit_events",
		"schema_migrations",
		"USERS",
		"Pairing_Codes",
	}

	for _, entity := range systemEntities {
		t.Run("Reject_"+entity, func(t *testing.T) {
			tbl, err := tableNameForEntity(entity)
			assert.Error(t, err)
			assert.Empty(t, tbl)
			assert.Contains(t, err.Error(), "cannot be rolled back via mutation rollback")
		})
	}
}

func TestTableNameForEntity_RejectsUnknownTables(t *testing.T) {
	unknownEntities := []string{
		"arbitrary_table",
		"admin_keys",
		"passwords",
		"drop table users",
	}

	for _, entity := range unknownEntities {
		t.Run("RejectUnknown_"+entity, func(t *testing.T) {
			tbl, err := tableNameForEntity(entity)
			assert.Error(t, err)
			assert.Empty(t, tbl)
			assert.Contains(t, err.Error(), "is not allowlisted for rollback")
		})
	}
}

func TestTableNameForEntity_AllowlistedEntities(t *testing.T) {
	allowlistMap := map[string]string{
		"topicmemory":          "topic_memories",
		"topic_memory":         "topic_memories",
		"entityprofile":        "entity_profiles",
		"sharedspace":          "shared_spaces",
		"sharedtaskreminder":   "shared_task_reminders",
		"scheduleevent":        "schedule_events",
		"babyprofile":          "baby_profiles",
		"babycarelog":          "baby_care_logs",
		"babyinventoryitem":    "baby_inventory_items",
		"housecontractrecord":  "house_contract_records",
		"homemaintenanceitem":  "home_maintenance_items",
		"vehicleasset":         "vehicle_assets",
		"financialtransaction": "financial_transactions",
		"categorybudget":       "category_budgets",
		"recurringbill":        "recurring_bills",
		"externalapiconnector": "external_api_connectors",
		"automationrule":       "automation_rules",
		"userpreference":       "user_preferences",
		"personalcrmcontact":   "personal_crm_contacts",
	}

	for entity, expectedTbl := range allowlistMap {
		t.Run("Allow_"+entity, func(t *testing.T) {
			tbl, err := tableNameForEntity(entity)
			require.NoError(t, err)
			assert.Equal(t, expectedTbl, tbl)
		})
	}
}
