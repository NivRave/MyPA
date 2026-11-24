package scheduler

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-63: a new cron rule created with a plain instruction is stored as a prompt: action and runs
// as a turn for its person when it fires. A plain rule stored before is left as it is, and
// /doctor counts it among the rules whose action does nothing.
func TestCronRule_APlainInstructionRunsAsATurn(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	var mu sync.Mutex
	var turns []string
	saved := startTurn
	startTurn = func(_ *orchestrator.Engine, _ *db.Client, userID, instruction, _ string) {
		mu.Lock()
		defer mu.Unlock()
		turns = append(turns, userID+": "+instruction)
	}
	t.Cleanup(func() { startTurn = saved })
	runner := automation.NewRunner(client, nil, automation.WithActionExecutor(func(context.Context, models.AutomationRule, events.MutationEvent) error {
		return nil
	}))

	label := "bl63-" + uuid.NewString()
	u := householdtest.Member(t, client, "1063-"+label, "Dana", "admin", label, "telegram")
	s, err := identity.ForUser(ctx, client, &u)
	require.NoError(t, err)
	call := orchestrator.CallContext{User: &u, Scope: s, DB: client, Extra: map[string]interface{}{}}

	// An existing rule with a plain instruction, stored before the fix.
	old := models.AutomationRule{UserID: u.PlatformID, HouseholdID: u.HouseholdID, Name: "Old plain", TriggerType: "cron",
		TriggerTopic: "*", ConditionJSON: `{"cron": "0 9 * * *"}`, ActionInstruction: "Summarise the week", IsActive: true}
	require.NoError(t, client.DB.Create(&old).Error)

	_, err = automation.NewModule(client).Execute(ctx, call, "create_automation_rule", map[string]interface{}{
		"name": "Weekly summary", "trigger_type": "cron", "trigger_topic": "*",
		"condition_json": `{"cron": "0 18 * * 5"}`, "action_instruction": "Summarise the week's spending"})
	require.NoError(t, err)

	var created models.AutomationRule
	require.NoError(t, client.DB.Where("household_id = ? AND name = ?", u.HouseholdID, "Weekly summary").First(&created).Error)
	assert.Equal(t, "prompt: Summarise the week's spending", created.ActionInstruction, "stored as a prompt: action")

	ReloadWorkflows(nil, client, runner, "Asia/Jerusalem")
	workflowMu.Lock()
	jobs := append([]job(nil), currentJobs...)
	workflowMu.Unlock()
	byName := map[string]job{}
	for _, j := range jobs {
		byName[j.name] = j
	}
	require.Contains(t, byName, ruleName(created))
	require.Contains(t, byName, ruleName(old))

	byName[ruleName(created)].run()
	byName[ruleName(old)].run()
	mu.Lock()
	assert.Equal(t, []string{u.PlatformID + ": Summarise the week's spending"}, turns, "the new rule runs as a turn; the old one is unchanged")
	mu.Unlock()

	var again models.AutomationRule
	require.NoError(t, client.DB.First(&again, old.ID).Error)
	assert.Equal(t, "Summarise the week", again.ActionInstruction, "an existing rule is not rewritten")

	n, err := client.IdleRuleCount(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "/doctor counts the existing rule whose action does nothing")
}
