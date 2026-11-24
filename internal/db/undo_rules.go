package db

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Who may undo what (BL-65). Undo is household-wide only for the household's shared data:
//
//   - In chat (/undo, undo_last_action) a person undoes their own changes only.
//   - In the dashboard an owner (identity.UndoOthersChanges) may also roll back another member's
//     change, or a change with no known person, to the household's shared data.
//   - A person-owned row (a personal memory, an unshared transaction or schedule event, a
//     scheduled workflow, a person's preferences or calendar settings) is rolled back by its own
//     person only, whoever wrote the change.
//
// RollbackSnapshot applies these rules to the locked log and to the row as it is and as it would
// be restored; the durability module applies them before it picks a log, so the selection and
// the rollback give the same answer.

// undoPersonKeyedTables are undoable tables without household_id: each row is one person's own.
var undoPersonKeyedTables = map[string]bool{"user_preferences": true, "topic_calendar_configs": true}

// workflowTrigger and workflowAction match a scheduled workflow, as workflowsOfOthersHidden does
// in SQL (automation.PromptOf).
var (
	workflowTrigger = regexp.MustCompile(`(?i)^\s*cron\s*$`)
	workflowAction  = regexp.MustCompile(`(?i)^\s*prompt:`)
)

// UndoRowState is a row of an undoable table, as stored or as a snapshot holds it, keyed by
// column name (the models' JSON names are their column names).
type UndoRowState map[string]interface{}

// personOwned reports whether the row is one person's own rather than the household's shared
// data, and whose: its user_id and person_id. A snapshot that lacks a sharing flag is read as the
// column's default, which is not shared.
func personOwned(table string, row UndoRowState) (owned bool, userKey, personID string) {
	userKey, personID = text(row["user_id"]), text(row["person_id"])
	switch {
	case undoPersonKeyedTables[table]:
		owned = true
	case table == "topic_memories":
		owned = !strings.EqualFold(strings.TrimSpace(text(row["scope"])), "family")
	case table == "financial_transactions", table == "schedule_events":
		owned = !truthy(row["is_shared"])
	case table == "automation_rules":
		owned = workflowTrigger.MatchString(text(row["trigger_type"])) && workflowAction.MatchString(text(row["action_instruction"]))
	}
	return owned, userKey, personID
}

// ownedByScope reports whether a person column pair names the scope's person: by person_id when it
// is filled, else by the old key (the caller's platform ID or users.id), as the dashboard's
// ownedBy reads it.
func ownedByScope(s identity.Scope, userKey, personID string) bool {
	if p := strings.TrimSpace(personID); p != "" {
		return s.PersonID() != "" && strings.EqualFold(p, s.PersonID())
	}
	u := strings.TrimSpace(userKey)
	return u != "" && (u == s.PersonKey() || u == s.PersonID())
}

// MutationIsOwn reports whether the mutation is the scope's person's own change.
func MutationIsOwn(s identity.Scope, log models.MutationLog) bool {
	pid := ""
	if log.PersonID != nil {
		pid = *log.PersonID
	}
	return ownedByScope(s, log.UserID, pid)
}

// MayUndo returns nil when the scope may roll back the mutation from via, given the states of the
// row it touches (as stored now and as it would be restored; nil states are skipped), and an
// error starting "permission denied" otherwise.
func MayUndo(s identity.Scope, via models.UndoVia, log models.MutationLog, table string, states ...UndoRowState) error {
	if !s.Valid() {
		return fmt.Errorf("permission denied: %w", identity.ErrNoScope)
	}
	own := MutationIsOwn(s, log)
	if !own && (via != models.UndoViaDashboard || !s.Can(identity.UndoOthersChanges)) {
		return fmt.Errorf("permission denied: mutation %q is not one of your changes", log.ID)
	}
	for _, st := range states {
		if st == nil {
			continue
		}
		owned, userKey, personID := personOwned(table, st)
		if !owned {
			continue
		}
		if !ownedByScope(s, userKey, personID) {
			return fmt.Errorf("permission denied: mutation %q changed a person's own data; only that person may undo it", log.ID)
		}
		if !own && (log.PersonID == nil || strings.TrimSpace(*log.PersonID) == "") {
			return fmt.Errorf("permission denied: mutation %q has no known person and changed a person's own data", log.ID)
		}
	}
	return nil
}

// text is a column value as a string; a uuid read into an interface may arrive as bytes.
func text(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case [16]byte:
		h := hex.EncodeToString(x[:])
		return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	case *string:
		if x == nil {
			return ""
		}
		return *x
	default:
		return fmt.Sprint(x)
	}
}

func truthy(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case *bool:
		return x != nil && *x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true") || strings.TrimSpace(x) == "t"
	}
	return false
}
