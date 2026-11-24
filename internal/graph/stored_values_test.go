package graph

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedBool reads one boolean column of one row as the database holds it: "true", "false" or "NULL".
func storedBool(t *testing.T, c *db.Client, table, column string, id any) string {
	t.Helper()
	var v sql.NullBool
	require.NoError(t, c.DB.Raw(fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", column, table), id).Row().Scan(&v))
	if !v.Valid {
		return "NULL"
	}
	return fmt.Sprint(v.Bool)
}

// BL-43, BL-62: each dashboard mutation stores the value the person chose, a false included, on
// create and on update, and answers with what it stored.
func TestDashboard_AFalseIsStoredAsAsked(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	c := resolver.DB
	h := dashboardServer(c)
	levi := "Levi-" + uuid.NewString()
	dana := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	omer := householdtest.Member(t, c, "1002-"+levi, "Omer", "family", levi, "telegram")
	S := signInAs(t, c, dana)

	do := func(query string, vars map[string]any) map[string]any {
		t.Helper()
		r := gql(t, h, S, query, vars)
		require.Empty(t, r.errs, query)
		for _, v := range r.data {
			return v.(map[string]any)
		}
		t.Fatalf("no data for %s", query)
		return nil
	}

	// financial_transactions.is_shared
	got := do(`mutation { logFinancialTransaction(type: "expense", category: "health", merchant: "Clinic", amount: 80, currency: "ILS", isShared: false) { id isShared } }`, nil)
	assert.Equal(t, "false", storedBool(t, c, "financial_transactions", "is_shared", got["id"]))
	assert.Equal(t, false, got["isShared"])

	// recurring_bills.auto_renew
	got = do(`mutation { saveRecurringBill(name: "Gym", category: "health", expectedAmount: 50, currency: "ILS", billingCycle: "monthly", nextDueDay: 3, autoRenew: false) { id autoRenew } }`, nil)
	assert.Equal(t, "false", storedBool(t, c, "recurring_bills", "auto_renew", got["id"]))
	assert.Equal(t, false, got["autoRenew"])

	// shared_spaces.can_view and can_edit: created false, updated to true and back to false.
	space := `mutation($v: Boolean!, $who: String!) { updateSharedSpace(topic: "finance", sharedWithUserId: $who, canView: $v, canEdit: $v, notifyMode: "silent") { id canView canEdit } }`
	var spaceID any
	for _, want := range []bool{false, true, false} {
		got = do(space, map[string]any{"v": want, "who": omer.PlatformID})
		if spaceID == nil {
			spaceID = got["id"]
		}
		require.Equal(t, spaceID, got["id"], "the same space is updated")
		assert.Equal(t, fmt.Sprint(want), storedBool(t, c, "shared_spaces", "can_view", spaceID))
		assert.Equal(t, fmt.Sprint(want), storedBool(t, c, "shared_spaces", "can_edit", spaceID))
		assert.Equal(t, want, got["canView"])
	}

	// automation_rules.is_active: created inactive; another created active, toggled off.
	got = do(`mutation { saveAutomationRule(name: "Off", triggerType: "cron", triggerTopic: "*", conditionJson: "{\"cron\": \"0 9 * * *\"}", actionInstruction: "notify: hi", isActive: false) { id isActive } }`, nil)
	assert.Equal(t, "false", storedBool(t, c, "automation_rules", "is_active", got["id"]))
	assert.Equal(t, false, got["isActive"])
	got = do(`mutation { saveAutomationRule(name: "On", triggerType: "cron", triggerTopic: "*", conditionJson: "{\"cron\": \"0 9 * * *\"}", actionInstruction: "notify: hi", isActive: true) { id } }`, nil)
	ruleID := got["id"]
	assert.Equal(t, "true", storedBool(t, c, "automation_rules", "is_active", ruleID))
	do(`mutation($id: ID!) { toggleAutomationRule(id: $id, isActive: false) { id } }`, map[string]any{"id": ruleID})
	assert.Equal(t, "false", storedBool(t, c, "automation_rules", "is_active", ruleID))

	// schedule_events.sync_to_google
	got = do(`mutation { createScheduleEvent(topic: "health", title: "Checkup", startTime: "2026-12-01T10:00:00Z", endTime: "2026-12-01T11:00:00Z", syncToGoogle: false) { id syncToGoogle } }`, nil)
	assert.Equal(t, "false", storedBool(t, c, "schedule_events", "sync_to_google", got["id"]))
	assert.Equal(t, false, got["syncToGoogle"])

	// topic_calendar_configs.include_in_morning_briefing: created false, updated to true and back.
	cfg := `mutation($v: Boolean!) { saveTopicCalendarConfig(topic: "health", includeInMorningBriefing: $v) { id includeInMorningBriefing } }`
	var cfgID any
	for _, want := range []bool{false, true, false} {
		got = do(cfg, map[string]any{"v": want})
		if cfgID == nil {
			cfgID = got["id"]
		}
		require.Equal(t, cfgID, got["id"], "the same config is updated")
		assert.Equal(t, fmt.Sprint(want), storedBool(t, c, "topic_calendar_configs", "include_in_morning_briefing", cfgID))
		assert.Equal(t, want, got["includeInMorningBriefing"])
	}
}
