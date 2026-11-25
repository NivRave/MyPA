package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// The undo guard (BL-68, the owner's decision 4): every chat tool the worker registers either only
// reads, or has a proof here that /undo right after it puts every undoable table back exactly as it
// was, or is on notUndoable with the reason it cannot be undone. A tool with an action parameter
// needs this for each action its description names. A new write tool, or a new action of one,
// without a proof fails TestUndoGuard_EveryToolIsClassified; a proof that /undo does not reverse
// fails TestUndoGuard_UndoReversesEveryWrite.

// readOnly are the tools that change nothing.
var readOnly = map[string]bool{
	"get_current_time": true, "list_recent_changes": true, "export_topic_data": true,
	"search_topic_memories": true, "get_entity_profile": true, "list_shared_activity": true,
	"list_calendar_events": true, "query_baby_logs_and_trends": true, "get_baby_milestone_suggestions": true,
	"list_house_contracts": true, "get_financial_summary": true, "list_external_apis": true,
	"list_automation_rules": true, "list_workflows": true, "get_user_preferences": true,
	"query_personal_crm": true, "search_emails": true, "read_email": true, "list_email_labels": true,
	"list_task_lists": true, "list_tasks": true, "search_contacts": true, "search_web": true, "fetch_webpage": true,
}

// readOnlyActions are the actions of a multi-action tool that change nothing.
var readOnlyActions = map[string]map[string]bool{
	"manage_recurring_bills":  {"list": true},
	"manage_home_maintenance": {"list": true, "suggest_seasonal": true},
	"manage_vehicle_asset":    {"list": true},
	"manage_baby_inventory":   {"list": true, "get": true, "check_low": true},
}

// notUndoable are the writes /undo cannot reverse, by design, each with its reason. Keep it short.
var notUndoable = map[string]string{
	"draft_email_reply":  "Gmail: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"archive_emails":     "Gmail: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"soft_delete_emails": "Gmail: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"apply_email_labels": "Gmail: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"create_email_label": "Gmail: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"unsubscribe_email":  "Gmail: an unsubscribe request goes out to the sender and cannot be called back",
	"create_task_list":   "Google Tasks: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"create_task":        "Google Tasks: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"complete_task":      "Google Tasks: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"delete_task":        "Google Tasks: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"create_contact":     "Google Contacts: the change is made in the person's Google account; MyPA keeps no copy to restore",
	"call_external_api":  "a request to an outside service: what it did there cannot be called back",
	"undo_last_action":   "undo itself: its own entry is written as already undone, and an undo is not undone",
	"schedule_reminder":  "a reminder to oneself is kept per person outside the household's data and is not on the undo allowlist (BL-69)",
}

// undoProof is one write of one tool: args returns its arguments, after making what the call needs.
type undoProof struct {
	tool   string
	action string // the action argument it covers, for a multi-action tool
	name   string
	args   func(e *undoEnv) map[string]interface{}
}

func (p undoProof) String() string {
	s := p.tool
	if p.action != "" {
		s += ":" + p.action
	}
	if p.name != "" {
		s += " (" + p.name + ")"
	}
	return s
}

// undoEnv is one household in a real database with the worker's tool modules, and Dana, an owner,
// writing in chat.
type undoEnv struct {
	ctx  context.Context
	c    *db.Client
	reg  *orchestrator.Registry
	dur  *durability.Module
	call orchestrator.CallContext
	n    int

	mu  sync.Mutex
	evs []events.MutationEvent
}

func newUndoEnv(t *testing.T, extra ...orchestrator.ToolModule) *undoEnv {
	t.Helper()
	c, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	bus := events.NewEventBus()
	t.Cleanup(func() { _ = bus.Close() })
	e := &undoEnv{ctx: context.Background(), c: c, dur: durability.NewModule(c, bus)}
	bus.SubscribeSync(func(_ context.Context, ev events.MutationEvent) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.evs = append(e.evs, ev)
		return nil
	})
	e.reg = orchestrator.NewRegistry()
	for _, m := range toolModules(toolDeps{db: c, bus: bus, durability: e.dur}) {
		e.reg.Register(m)
	}
	for _, m := range extra {
		e.reg.Register(m)
	}
	label := "Undo-" + uuid.NewString()
	dana := householdtest.Member(t, c, "dana-"+label, "Dana", "admin", label, "telegram")
	s, err := identity.ForUser(e.ctx, c, &dana)
	require.NoError(t, err)
	e.call = orchestrator.CallContext{User: &dana, Scope: s, DB: c, EventBus: bus, Extra: map[string]interface{}{}}
	return e
}

// run calls a tool as Dana; a setup call must succeed.
func (e *undoEnv) run(tool string, args map[string]interface{}) (string, error) {
	out, _, err := e.reg.Execute(e.ctx, e.call, tool, args)
	return out, err
}

func (e *undoEnv) must(tool string, args map[string]interface{}) {
	if _, err := e.run(tool, args); err != nil {
		panic(fmt.Sprintf("setup %s: %v", tool, err))
	}
}

// unique is a name no other proof uses.
func (e *undoEnv) unique(s string) string {
	e.n++
	return fmt.Sprintf("%s %d", s, e.n)
}

// newest is the ID of the newest row of a table of the household, as a JSON number.
func (e *undoEnv) newest(table string) float64 {
	var id uint
	if err := e.c.DB.Table(table).Select("id").Where("household_id = ?", e.call.Scope.HouseholdID()).
		Order("id DESC").Limit(1).Scan(&id).Error; err != nil || id == 0 {
		panic(fmt.Sprintf("no row in %s: %v", table, err))
	}
	return float64(id)
}

func (e *undoEnv) recorded() []events.MutationEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]events.MutationEvent(nil), e.evs...)
}

// undoableTables is every table undo may change.
func undoableTables() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range durability.AllowedRollbackEntities {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// tables is every undoable table as it is now, each row as JSON, in a stable order. A memory's
// vector is left out: undo marks a restored memory as needing a new one (BL-57).
func (e *undoEnv) tables() (map[string][]string, error) {
	out := map[string][]string{}
	for _, t := range undoableTables() {
		var rows []map[string]interface{}
		if err := e.c.DB.Table(t).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("read %s: %w", t, err)
		}
		for _, r := range rows {
			delete(r, "embedding")
			b, err := json.Marshal(r)
			if err != nil {
				return nil, err
			}
			out[t] = append(out[t], string(b))
		}
		sort.Strings(out[t])
	}
	return out, nil
}

func diff(before, after map[string][]string) string {
	var sb strings.Builder
	for _, t := range undoableTables() {
		b, a := strings.Join(before[t], "\n"), strings.Join(after[t], "\n")
		if a != b {
			fmt.Fprintf(&sb, "%s:\n  before: %s\n  after:  %s\n", t, b, a)
		}
	}
	return sb.String()
}

// prove calls the tool, then /undo, and returns why /undo did not reverse it, or nil.
func (e *undoEnv) prove(p undoProof) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	args := p.args(e)
	before, err := e.tables()
	if err != nil {
		return err
	}
	mark := len(e.recorded())
	if _, err := e.run(p.tool, args); err != nil {
		return fmt.Errorf("the call failed: %w", err)
	}
	evs := e.recorded()[mark:]
	if len(evs) == 0 {
		return fmt.Errorf("the call published no change")
	}
	changed, err := e.tables()
	if err != nil {
		return err
	}
	if diff(before, changed) == "" {
		return fmt.Errorf("the call changed no undoable table: the proof proves nothing")
	}
	res, err := e.dur.RollbackLast(e.ctx, e.call.Scope)
	if err != nil {
		return fmt.Errorf("/undo failed: %w", err)
	}
	if last := evs[len(evs)-1]; res.MutationID != last.ID {
		return fmt.Errorf("/undo took change %s (%s), not the call's own %s (%s): %s", res.MutationID, res.Summary, last.ID, last.Summary, res.Detail)
	}
	after, err := e.tables()
	if err != nil {
		return err
	}
	if d := diff(before, after); d != "" {
		return fmt.Errorf("/undo did not put everything back (%s):\n%s", res.Detail, d)
	}
	return nil
}

// undoProofs are the proofs of every write tool the worker registers.
var undoProofs = []undoProof{
	// Memory
	{tool: "save_topic_memory", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"topic": "family", "fact": e.unique("Noa likes oranges"), "scope": "family"}
	}},
	{tool: "save_topic_memory", name: "in place of an older one", args: func(e *undoEnv) map[string]interface{} {
		e.must("save_topic_memory", map[string]interface{}{"topic": "family", "fact": e.unique("The plumber is Avi"), "scope": "family"})
		return map[string]interface{}{"topic": "family", "fact": e.unique("The plumber is Moshe"), "scope": "family",
			"supersedes_memory_id": e.newest("topic_memories")}
	}},
	{tool: "upsert_entity_profile", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"topic": "family", "entity_type": "person", "entity_name": e.unique("Grandma"),
			"attributes": map[string]interface{}{"birthday": "1950-03-01"}}
	}},
	{tool: "upsert_entity_profile", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Grandpa")
		e.must("upsert_entity_profile", map[string]interface{}{"topic": "family", "entity_type": "person", "entity_name": name,
			"attributes": map[string]interface{}{"birthday": "1948-01-01"}})
		return map[string]interface{}{"topic": "family", "entity_type": "person", "entity_name": name,
			"attributes": map[string]interface{}{"birthday": "1948-02-02"}}
	}},
	// Shared
	{tool: "configure_shared_topic", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"topic": "health", "notify_mode": "digest"}
	}},
	{tool: "configure_shared_topic", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		e.must("configure_shared_topic", map[string]interface{}{"topic": "work", "notify_mode": "instant"})
		return map[string]interface{}{"topic": "work", "notify_mode": "silent"}
	}},
	{tool: "create_shared_reminder", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"title": e.unique("Buy milk"), "topic": "family"}
	}},
	{tool: "complete_shared_reminder", args: func(e *undoEnv) map[string]interface{} {
		e.must("create_shared_reminder", map[string]interface{}{"title": e.unique("Call the bank"), "topic": "family"})
		return map[string]interface{}{"id": e.newest("shared_task_reminders")}
	}},
	// Calendar
	{tool: "create_calendar_event", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"title": e.unique("Dentist"), "start_time": "2026-12-01T10:00:00Z",
			"end_time": "2026-12-01T11:00:00Z", "sync_to_google": false}
	}},
	{tool: "update_calendar_event", args: func(e *undoEnv) map[string]interface{} {
		e.must("create_calendar_event", map[string]interface{}{"title": e.unique("Haircut"), "start_time": "2026-12-02T10:00:00Z",
			"end_time": "2026-12-02T11:00:00Z", "sync_to_google": false})
		return map[string]interface{}{"event_id": e.newest("schedule_events"), "title": e.unique("Haircut moved")}
	}},
	{tool: "delete_calendar_event", args: func(e *undoEnv) map[string]interface{} {
		e.must("create_calendar_event", map[string]interface{}{"title": e.unique("Yoga"), "start_time": "2026-12-03T10:00:00Z",
			"end_time": "2026-12-03T11:00:00Z", "sync_to_google": false})
		return map[string]interface{}{"event_id": e.newest("schedule_events")}
	}},
	{tool: "configure_topic_calendar", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"topic": "health", "color_id": "4"}
	}},
	{tool: "configure_topic_calendar", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		e.must("configure_topic_calendar", map[string]interface{}{"topic": "work", "color_id": "2"})
		return map[string]interface{}{"topic": "work", "color_id": "9", "prefix_tag": "Job"}
	}},
	// Baby
	{tool: "upsert_baby_profile", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"name": e.unique("Leo"), "date_of_birth": "2026-05-10"}
	}},
	{tool: "upsert_baby_profile", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Maya")
		e.must("upsert_baby_profile", map[string]interface{}{"name": name, "date_of_birth": "2026-04-01"})
		return map[string]interface{}{"name": name, "date_of_birth": "2026-04-01", "current_weight_grams": float64(5200)}
	}},
	{tool: "log_baby_care", name: "feeding", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Ari")
		e.must("upsert_baby_profile", map[string]interface{}{"name": name, "date_of_birth": "2026-06-01"})
		return map[string]interface{}{"baby_name": name, "log_type": "feeding", "value_numeric": 120.0, "unit": "ml"}
	}},
	{tool: "log_baby_care", name: "a measurement, which also changes the profile", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Tal")
		e.must("upsert_baby_profile", map[string]interface{}{"name": name, "date_of_birth": "2026-06-01", "current_weight_grams": float64(4000)})
		return map[string]interface{}{"baby_name": name, "log_type": "measurement", "value_numeric": 5.1, "unit": "kg"}
	}},
	{tool: "manage_baby_inventory", action: "set", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"action": "set", "item_name": e.unique("Wipes"), "quantity": 4.0, "unit": "packs", "low_threshold": 1.0}
	}},
	{tool: "manage_baby_inventory", action: "set", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Formula")
		e.must("manage_baby_inventory", map[string]interface{}{"action": "set", "item_name": name, "quantity": 3.0, "unit": "cans"})
		return map[string]interface{}{"action": "set", "item_name": name, "quantity": 6.0, "unit": "cans"}
	}},
	{tool: "manage_baby_inventory", action: "adjust", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Diapers")
		e.must("manage_baby_inventory", map[string]interface{}{"action": "set", "item_name": name, "quantity": 30.0, "unit": "pcs"})
		return map[string]interface{}{"action": "adjust", "item_name": name, "delta": -2.0}
	}},
	{tool: "manage_baby_inventory", action: "adjust", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"action": "adjust", "item_name": e.unique("Drops"), "delta": 2.0, "unit": "bottles"}
	}},
	// House
	{tool: "save_house_contract_or_receipt", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"record_type": "lease", "title": e.unique("Flat lease"), "vendor_or_landlord": "Dan", "amount": 5000.0}
	}},
	{tool: "save_house_contract_or_receipt", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		title := e.unique("Boiler warranty")
		e.must("save_house_contract_or_receipt", map[string]interface{}{"record_type": "warranty", "title": title, "vendor_or_landlord": "Hot Co"})
		return map[string]interface{}{"contract_id": e.newest("house_contract_records"), "record_type": "warranty", "title": title,
			"vendor_or_landlord": "Hot Co", "amount": 300.0}
	}},
	{tool: "manage_home_maintenance", action: "create", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"action": "create", "task_name": e.unique("Clean filters"), "interval_days": 90.0}
	}},
	{tool: "manage_home_maintenance", action: "complete", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_home_maintenance", map[string]interface{}{"action": "create", "task_name": e.unique("Gutters"), "interval_days": 180.0})
		return map[string]interface{}{"action": "complete", "item_id": e.newest("home_maintenance_items")}
	}},
	{tool: "manage_home_maintenance", action: "update", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_home_maintenance", map[string]interface{}{"action": "create", "task_name": e.unique("Pest control"), "interval_days": 30.0})
		return map[string]interface{}{"action": "update", "item_id": e.newest("home_maintenance_items"), "interval_days": 60.0}
	}},
	{tool: "manage_home_maintenance", action: "delete", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_home_maintenance", map[string]interface{}{"action": "create", "task_name": e.unique("Paint fence"), "interval_days": 365.0})
		return map[string]interface{}{"action": "delete", "item_id": e.newest("home_maintenance_items")}
	}},
	{tool: "manage_vehicle_asset", action: "create", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"action": "create", "name": e.unique("Mazda"), "current_mileage_km": 10000.0}
	}},
	{tool: "manage_vehicle_asset", action: "update", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_vehicle_asset", map[string]interface{}{"action": "create", "name": e.unique("Kia"), "current_mileage_km": 5000.0})
		return map[string]interface{}{"action": "update", "asset_id": e.newest("vehicle_assets"), "notes": "new tyres"}
	}},
	{tool: "manage_vehicle_asset", action: "update_mileage", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_vehicle_asset", map[string]interface{}{"action": "create", "name": e.unique("Skoda"), "current_mileage_km": 7000.0})
		return map[string]interface{}{"action": "update_mileage", "asset_id": e.newest("vehicle_assets"), "current_mileage_km": 7500.0}
	}},
	{tool: "manage_vehicle_asset", action: "log_service", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_vehicle_asset", map[string]interface{}{"action": "create", "name": e.unique("Fiat"), "current_mileage_km": 20000.0})
		return map[string]interface{}{"action": "log_service", "asset_id": e.newest("vehicle_assets"), "current_mileage_km": 21000.0, "notes": "oil"}
	}},
	{tool: "manage_vehicle_asset", action: "delete", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_vehicle_asset", map[string]interface{}{"action": "create", "name": e.unique("Seat")})
		return map[string]interface{}{"action": "delete", "asset_id": e.newest("vehicle_assets")}
	}},
	// Finance
	{tool: "log_financial_transaction", action: "log", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"amount": 42.0, "category": "groceries", "merchant": e.unique("Shufersal")}
	}},
	{tool: "log_financial_transaction", action: "update", args: func(e *undoEnv) map[string]interface{} {
		e.must("log_financial_transaction", map[string]interface{}{"amount": 18.0, "category": "dining", "merchant": e.unique("Cafe")})
		return map[string]interface{}{"action": "update", "transaction_id": e.newest("financial_transactions"), "amount": 21.0, "is_shared": false}
	}},
	{tool: "log_financial_transaction", action: "delete", args: func(e *undoEnv) map[string]interface{} {
		e.must("log_financial_transaction", map[string]interface{}{"amount": 9.0, "category": "other", "merchant": e.unique("Kiosk")})
		return map[string]interface{}{"action": "delete", "transaction_id": e.newest("financial_transactions")}
	}},
	{tool: "set_category_budget", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"category": "groceries", "monthly_limit": 1500.0}
	}},
	{tool: "set_category_budget", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		e.must("set_category_budget", map[string]interface{}{"category": "dining", "monthly_limit": 400.0})
		return map[string]interface{}{"category": "dining", "monthly_limit": 600.0, "alert_threshold_pct": 90.0}
	}},
	{tool: "manage_recurring_bills", action: "add", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"action": "add", "name": e.unique("Netflix"), "expected_amount": 50.0}
	}},
	{tool: "manage_recurring_bills", action: "update", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_recurring_bills", map[string]interface{}{"action": "add", "name": e.unique("Gym"), "expected_amount": 150.0})
		return map[string]interface{}{"action": "update", "bill_id": e.newest("recurring_bills"), "expected_amount": 170.0}
	}},
	{tool: "manage_recurring_bills", action: "delete", args: func(e *undoEnv) map[string]interface{} {
		e.must("manage_recurring_bills", map[string]interface{}{"action": "add", "name": e.unique("Spotify"), "expected_amount": 20.0})
		return map[string]interface{}{"action": "delete", "bill_id": e.newest("recurring_bills")}
	}},
	// Automation
	{tool: "register_external_api", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"name": strings.ReplaceAll(e.unique("weather_api"), " ", "_"), "base_url": "https://api.example.com"}
	}},
	{tool: "create_automation_rule", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"name": e.unique("Big spend"), "trigger_type": "event_mutation", "trigger_topic": "finance",
			"action_instruction": "notify: a big expense"}
	}},
	{tool: "delete_automation_rule", args: func(e *undoEnv) map[string]interface{} {
		e.must("create_automation_rule", map[string]interface{}{"name": e.unique("Baby alert"), "trigger_type": "event_mutation",
			"trigger_topic": "baby", "action_instruction": "notify: baby update"})
		return map[string]interface{}{"rule_id": e.newest("automation_rules")}
	}},
	{tool: "schedule_workflow", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"name": e.unique("Morning summary"), "cron_expression": "0 7 * * *", "instruction": "Summarise my day"}
	}},
	{tool: "delete_workflow", args: func(e *undoEnv) map[string]interface{} {
		e.must("schedule_workflow", map[string]interface{}{"name": e.unique("Weekly plan"), "cron_expression": "0 8 * * 1", "instruction": "Plan my week"})
		return map[string]interface{}{"id": fmt.Sprint(e.newest("automation_rules"))}
	}},
	// Preferences and the personal CRM
	{tool: "update_user_preferences", name: "new", args: func(e *undoEnv) map[string]interface{} {
		var n int64
		e.c.DB.Table("user_preferences").Count(&n)
		if n > 0 {
			panic("the household already has preferences: run the new-preferences proof first")
		}
		return map[string]interface{}{"tone_persona": "Warm"}
	}},
	{tool: "update_user_preferences", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		e.must("update_user_preferences", map[string]interface{}{"tone_persona": "Concise"})
		return map[string]interface{}{"tone_persona": "Humorous", "preferred_language": "Hebrew"}
	}},
	{tool: "upsert_personal_crm", name: "new", args: func(e *undoEnv) map[string]interface{} {
		return map[string]interface{}{"person_name": e.unique("Aunt Rina"), "relationship": "Aunt"}
	}},
	{tool: "upsert_personal_crm", name: "existing", args: func(e *undoEnv) map[string]interface{} {
		name := e.unique("Uncle Avi")
		e.must("upsert_personal_crm", map[string]interface{}{"person_name": name, "relationship": "Uncle"})
		return map[string]interface{}{"id": e.newest("personal_crm_contacts"), "person_name": name, "relationship": "Uncle", "gift_ideas_and_sizes": "M"}
	}},
}

// quoted are the 'words' an action parameter's description names.
var quoted = regexp.MustCompile(`'([a-z_]+)'`)

// classify returns every tool of decls, or action of one, that is neither read-only, proven
// undoable by proofs, nor on notUndoable.
func classify(decls []*genai.FunctionDeclaration, proofs []undoProof) []string {
	proven := map[string]bool{}
	for _, p := range proofs {
		proven[p.tool] = true
		proven[p.tool+":"+p.action] = true
	}
	var missing []string
	for _, d := range decls {
		if readOnly[d.Name] || notUndoable[d.Name] != "" {
			continue
		}
		if !proven[d.Name] {
			missing = append(missing, d.Name)
			continue
		}
		if d.Parameters == nil || d.Parameters.Properties["action"] == nil {
			continue
		}
		for _, m := range quoted.FindAllStringSubmatch(d.Parameters.Properties["action"].Description, -1) {
			if !readOnlyActions[d.Name][m[1]] && !proven[d.Name+":"+m[1]] {
				missing = append(missing, d.Name+":"+m[1])
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// BL-68, decision 4: every tool the worker registers is classified, and every action a
// multi-action tool names.
func TestUndoGuard_EveryToolIsClassified(t *testing.T) {
	reg := orchestrator.NewRegistry()
	for _, m := range toolModules(toolDeps{db: &db.Client{}, durability: durability.NewModule(nil)}) {
		reg.Register(m)
	}
	decls := append(reg.AllDeclarations(), &genai.FunctionDeclaration{Name: "get_current_time"})
	assert.Empty(t, classify(decls, undoProofs),
		"a tool that writes needs a proof in undoProofs that /undo reverses it, or a reason in notUndoable")

	for tool, reason := range notUndoable {
		assert.NotEmpty(t, strings.TrimSpace(reason), tool)
	}
	assert.LessOrEqual(t, len(notUndoable), 15, "the list of writes that cannot be undone stays short")
}

// BL-68, decisions 1 and 4: /undo right after each write puts every undoable table back as it was.
func TestUndoGuard_UndoReversesEveryWrite(t *testing.T) {
	e := newUndoEnv(t)
	for _, p := range undoProofs {
		t.Run(p.String(), func(t *testing.T) {
			assert.NoError(t, e.prove(p))
		})
	}
}

// BL-68, decision 2: a write now logged as create, update or delete keeps the name people and
// rules have always seen (Label): the partner notifier's verb and a rule's action condition read it.
func TestUndoGuard_PeopleAndRulesSeeTheSameNames(t *testing.T) {
	e := newUndoEnv(t)
	for _, c := range []struct {
		tool  string
		args  map[string]interface{}
		label string
	}{
		{"log_financial_transaction", map[string]interface{}{"amount": 12.0, "category": "dining", "merchant": "Label Cafe"}, "transaction_logged"},
		{"set_category_budget", map[string]interface{}{"category": "health", "monthly_limit": 100.0}, "budget_set"},
		{"manage_baby_inventory", map[string]interface{}{"action": "set", "item_name": "Label Wipes", "quantity": 2.0}, "update"},
		{"manage_home_maintenance", map[string]interface{}{"action": "create", "task_name": "Label filters"}, "maintenance_created"},
		{"create_shared_reminder", map[string]interface{}{"title": "Label milk"}, "create"},
	} {
		mark := len(e.recorded())
		_, err := e.run(c.tool, c.args)
		require.NoError(t, err, c.tool)
		evs := e.recorded()[mark:]
		require.NotEmpty(t, evs, c.tool)
		assert.Equal(t, c.label, evs[len(evs)-1].Label(), c.tool)
	}
}

// rogue is a tool module whose one tool writes a row and logs it with an action undo cannot
// reverse: what a new write tool without an undo looks like.
type rogue struct{ c *db.Client }

func (rogue) Name() string { return "rogue" }
func (rogue) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{{Name: "rogue_write", Description: "Writes a care log.", Parameters: &genai.Schema{Type: genai.TypeObject}}}
}
func (rogue) SystemPromptSection(context.Context, orchestrator.CallContext) string { return "" }
func (r rogue) Execute(ctx context.Context, call orchestrator.CallContext, _ string, _ map[string]interface{}) (string, error) {
	row := models.BabyCareLog{BabyID: 1, HouseholdID: call.Scope.HouseholdID(), FamilyGroup: call.Scope.LegacyLabel(),
		LogType: "feeding", Timestamp: time.Now(), LoggedByUserID: call.User.ID}
	if err := r.c.DB.Create(&row).Error; err != nil {
		return "", err
	}
	return "logged", call.EventBus.Publish(ctx, events.MutationEvent{Topic: "baby", Action: "rogue_logged", EntityType: "BabyCareLog",
		EntityID: fmt.Sprint(row.ID), ActorUserID: call.User.ID, HouseholdID: call.Scope.HouseholdID(), Summary: "Rogue care log"})
}

// The guard catches a write tool with no undo: unlisted, it fails the classification; given a
// proof, the proof fails, because /undo cannot reverse it.
func TestUndoGuard_CatchesAWriteToolWithNoUndo(t *testing.T) {
	r := &rogue{}
	e := newUndoEnv(t, r)
	r.c = e.c
	assert.Contains(t, classify(e.reg.AllDeclarations(), undoProofs), "rogue_write", "an unlisted write tool fails the guard")

	err := e.prove(undoProof{tool: "rogue_write", args: func(*undoEnv) map[string]interface{} { return map[string]interface{}{} }})
	require.Error(t, err, "a proof of a write /undo cannot reverse fails")
	t.Log(err)
}
