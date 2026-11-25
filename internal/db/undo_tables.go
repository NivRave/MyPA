package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
)

// UndoTables is the complete, authoritative mapping of the entity types undo may roll back to their
// tables (the undo allowlist). The durability module exposes it as AllowedRollbackEntities.
var UndoTables = map[string]string{
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

// ResolveUndoTable validates an entity type against the undo allowlist and returns its table.
func ResolveUndoTable(entityType string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(entityType))
	switch norm {
	case "users", "pairing_codes", "households", "household", "encrypted_credentials", "audit_sessions", "audit_events", "schema_migrations",
		// V4-32, V4-35: identity and sign-ins are never changed by undo (ADR 0002 §10).
		"people", "person", "channel_accounts", "channel_account", "memberships", "membership", "access_tokens", "access_token",
		"sessions", "session":
		return "", fmt.Errorf("system entity %q cannot be rolled back via mutation rollback", entityType)
	}
	tbl, ok := UndoTables[norm]
	if !ok {
		return "", fmt.Errorf("unknown entity type %q is not allowlisted for rollback", entityType)
	}
	return tbl, nil
}

// undoModels are the models of the undoable tables, for what an older snapshot of one may lack
// (clearOmitted, BL-68). TestUndoModels_EveryUndoableTableHasItsModel keeps it complete.
var undoModels = map[string]interface{}{
	"topic_memories":          models.TopicMemory{},
	"entity_profiles":         models.EntityProfile{},
	"shared_spaces":           models.SharedSpace{},
	"shared_task_reminders":   models.SharedTaskReminder{},
	"schedule_events":         models.ScheduleEvent{},
	"topic_calendar_configs":  models.TopicCalendarConfig{},
	"baby_profiles":           models.BabyProfile{},
	"baby_care_logs":          models.BabyCareLog{},
	"baby_inventory_items":    models.BabyInventoryItem{},
	"house_contract_records":  models.HouseContractRecord{},
	"home_maintenance_items":  models.HomeMaintenanceItem{},
	"vehicle_assets":          models.VehicleAsset{},
	"financial_transactions":  models.FinancialTransaction{},
	"category_budgets":        models.CategoryBudget{},
	"recurring_bills":         models.RecurringBill{},
	"external_api_connectors": models.ExternalAPIConnector{},
	"automation_rules":        models.AutomationRule{},
	"user_preferences":        models.UserPreference{},
	"personal_crm_contacts":   models.PersonalCRMContact{},
}

// undoKeyColumn is the column an undoable table's rows are found by: id, except for a table keyed
// by its person.
func undoKeyColumn(table string) string {
	if table == "user_preferences" {
		return "user_id"
	}
	return "id"
}

// legacyActions are the actions the chat tools logged before BL-68, and the change each made.
// An empty op is a change whose old entry cannot say what it did: an upsert logged without whether
// the row existed, or a memory saved in place of an older one without the older one's state.
var legacyActions = map[string]string{
	"transaction_logged": "create", "rule_created": "create", "register_connector": "create",
	"maintenance_created": "create", "vehicle_created": "create", "recurring_bill_added": "create",
	"contract_saved": "create",

	"maintenance_updated": "update", "maintenance_completed": "update", "vehicle_updated": "update",
	"vehicle_mileage_updated": "update", "vehicle_service_logged": "update", "contract_updated": "update",
	"recurring_bill_updated": "update", "complete": "update", "preferences_updated": "update",

	"maintenance_deleted": "delete", "vehicle_deleted": "delete", "recurring_bill_deleted": "delete",
	"rule_deleted": "delete",

	"upsert": "", "budget_set": "", "configure": "", "supersede": "",
}

// rowChangeActions are the actions of an entry that changed a row: create, update, delete and the
// older names of those. Anything else (rule_executed, undo) changed nothing undo could reverse.
func rowChangeActions() []string {
	out := []string{"create", "update", "delete"}
	for a := range legacyActions {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// ErrCannotUndo marks a change undo cannot reverse: its entry lacks what undo needs (an entry
// written before BL-68). Nothing is changed; CannotUndoMessage says which change it is.
var ErrCannotUndo = errors.New("this change cannot be undone")

// usableSnapshot reports whether a snapshot holds a row to restore.
func usableSnapshot(js string) bool {
	var m map[string]interface{}
	return strings.TrimSpace(js) != "" && json.Unmarshal([]byte(js), &m) == nil && len(m) > 0
}

// UndoPlan is how undo reverses a mutation: the change to reverse ("create", "update" or "delete")
// and, for an update or a delete, the snapshot to restore. An entry written before BL-68 under an
// old action name is read by the change that name stood for, where its snapshot is enough; a delete
// logged then may have kept the deleted row as its after-image. Anything else is ErrCannotUndo.
func UndoPlan(log models.MutationLog) (op, restore string, err error) {
	op = strings.ToLower(strings.TrimSpace(log.Action))
	legacy := false
	if mapped, ok := legacyActions[op]; ok {
		op, legacy = mapped, true
	}
	switch op {
	case "create":
		if strings.TrimSpace(log.EntityID) == "" {
			return "", "", fmt.Errorf("%w: the entry names no row", ErrCannotUndo)
		}
		return op, "", nil
	case "update":
		if strings.TrimSpace(log.EntityID) == "" || !usableSnapshot(log.BeforeJSON) {
			return "", "", fmt.Errorf("%w: the entry kept no earlier state", ErrCannotUndo)
		}
		return op, log.BeforeJSON, nil
	case "delete":
		switch {
		case usableSnapshot(log.BeforeJSON):
			return op, log.BeforeJSON, nil
		case legacy && usableSnapshot(log.AfterJSON):
			return op, log.AfterJSON, nil
		}
		return "", "", fmt.Errorf("%w: the entry kept no copy of the deleted row", ErrCannotUndo)
	}
	return "", "", fmt.Errorf("%w: undo does not know how to reverse %q", ErrCannotUndo, log.Action)
}

// CannotUndoMessage is what /undo answers for a change it cannot reverse: which change, and that
// nothing was changed (BL-68, the owner's decision 3).
func CannotUndoMessage(log models.MutationLog) string {
	what := strings.TrimSpace(log.Summary)
	if what == "" {
		what = fmt.Sprintf("%s %s", log.Label(), log.EntityType)
	}
	when := ""
	if !log.CreatedAt.IsZero() {
		when = " (" + log.CreatedAt.UTC().Format(time.RFC822) + ")"
	}
	return fmt.Sprintf("I can't undo %q%s: it was recorded before undo kept what it needs to reverse that kind of change. "+
		"Nothing was changed.", what, when)
}

// undoAlsoOf reads the other rows a change touched (BL-68).
func undoAlsoOf(log models.MutationLog) ([]models.UndoAlso, error) {
	if log.AlsoJSON == nil || strings.TrimSpace(*log.AlsoJSON) == "" {
		return nil, nil
	}
	var also []models.UndoAlso
	if err := json.Unmarshal([]byte(*log.AlsoJSON), &also); err != nil {
		return nil, fmt.Errorf("failed to parse the other rows mutation %s touched: %w", log.ID, err)
	}
	return also, nil
}
