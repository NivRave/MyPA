package durability

import (
	"fmt"
	"strings"
)

// AllowedRollbackEntities defines the complete, authoritative mapping of domain entity types to physical database tables.
var AllowedRollbackEntities = map[string]string{
	"topicmemory":             "topic_memories",
	"topic_memory":            "topic_memories",
	"topic_memories":          "topic_memories",
	"entityprofile":           "entity_profiles",
	"entity_profile":          "entity_profiles",
	"entity_profiles":         "entity_profiles",
	"sharedspace":             "shared_spaces",
	"shared_space":            "shared_spaces",
	"shared_spaces":           "shared_spaces",
	"sharedtaskreminder":      "shared_task_reminders",
	"shared_task":             "shared_task_reminders",
	"shared_task_reminder":    "shared_task_reminders",
	"shared_task_reminders":   "shared_task_reminders",
	"scheduleevent":           "schedule_events",
	"schedule_event":          "schedule_events",
	"schedule_events":         "schedule_events",
	"topiccalendarconfig":     "topic_calendar_configs",
	"topic_calendar_config":   "topic_calendar_configs",
	"topic_calendar_configs":  "topic_calendar_configs",
	"babyprofile":             "baby_profiles",
	"baby_profile":            "baby_profiles",
	"baby_profiles":           "baby_profiles",
	"babycarelog":             "baby_care_logs",
	"baby_care_log":           "baby_care_logs",
	"baby_care_logs":          "baby_care_logs",
	"babyinventoryitem":       "baby_inventory_items",
	"baby_inventory_item":     "baby_inventory_items",
	"baby_inventory_items":    "baby_inventory_items",
	"housecontractrecord":     "house_contract_records",
	"house_contract_record":   "house_contract_records",
	"house_contracts":         "house_contract_records",
	"house_contract_records":  "house_contract_records",
	"homemaintenanceitem":     "home_maintenance_items",
	"home_maintenance_item":   "home_maintenance_items",
	"home_maintenance_items":  "home_maintenance_items",
	"vehicleasset":            "vehicle_assets",
	"vehicle_asset":           "vehicle_assets",
	"vehicle_assets":          "vehicle_assets",
	"financialtransaction":    "financial_transactions",
	"financial_transaction":   "financial_transactions",
	"financial_transactions":  "financial_transactions",
	"categorybudget":          "category_budgets",
	"category_budget":         "category_budgets",
	"category_budgets":        "category_budgets",
	"recurringbill":           "recurring_bills",
	"recurring_bill":          "recurring_bills",
	"recurring_bills":         "recurring_bills",
	"externalapiconnector":    "external_api_connectors",
	"external_api_connector":  "external_api_connectors",
	"external_api_connectors": "external_api_connectors",
	"automationrule":          "automation_rules",
	"automation_rule":         "automation_rules",
	"automation_rules":        "automation_rules",
	"userpreference":          "user_preferences",
	"user_preference":         "user_preferences",
	"user_preferences":        "user_preferences",
	"personalcrmcontact":      "personal_crm_contacts",
	"personal_crm_contact":    "personal_crm_contacts",
	"personal_crm_contacts":   "personal_crm_contacts",
}

// ResolveRollbackTable validates entityType against allowlist and returns the physical table name.
func ResolveRollbackTable(entityType string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(entityType))
	switch norm {
	case "users", "pairing_codes", "households", "household", "encrypted_credentials", "audit_sessions", "audit_events", "schema_migrations":
		return "", fmt.Errorf("system entity %q cannot be rolled back via mutation rollback", entityType)
	}

	tbl, ok := AllowedRollbackEntities[norm]
	if !ok {
		return "", fmt.Errorf("unknown entity type %q is not allowlisted for rollback", entityType)
	}
	return tbl, nil
}
