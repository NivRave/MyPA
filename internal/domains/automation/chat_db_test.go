package automation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-62: a rule created in chat with is_active false is stored inactive, and the scheduler and
// the event runner (ListActiveAutomationRules) do not get it.
func TestChat_ARuleCreatedInactiveIsStoredInactive(t *testing.T) {
	c := newTestDBClient(t)
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	u := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	s, err := identity.ForUser(ctx, c, &u)
	require.NoError(t, err)
	call := orchestrator.CallContext{User: &u, Scope: s, DB: c, Extra: map[string]interface{}{}}

	mod := automation.NewModule(c)
	_, err = mod.Execute(ctx, call, "create_automation_rule", map[string]interface{}{
		"name": "Off BL62", "trigger_type": "cron", "condition_json": `{"cron": "0 9 * * *"}`,
		"action_instruction": "notify: hi", "is_active": false})
	require.NoError(t, err)

	var rule models.AutomationRule
	require.NoError(t, c.DB.Where("household_id = ? AND name = ?", u.HouseholdID, "Off BL62").First(&rule).Error)
	assert.False(t, rule.IsActive, "stored inactive")

	active, err := c.ListActiveAutomationRules("*")
	require.NoError(t, err)
	for _, r := range active {
		assert.NotEqual(t, rule.ID, r.ID, "an inactive rule is not scheduled or run")
	}
}
