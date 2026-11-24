package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workflowCaller(t *testing.T, label, person string) orchestrator.CallContext {
	t.Helper()
	return orchestrator.CallContext{
		User:  &models.User{PlatformID: person, FamilyGroup: label},
		Scope: identitytest.Scope(t, label, person, "family"),
	}
}

// V4-07: schedule_workflow writes a cron automation rule of the caller's household whose action is
// the instruction as a prompt; the scheduler is asked to reload. The answer is the one the tool
// gave before.
func TestScheduleWorkflow_WritesACronRuleWithAPrompt(t *testing.T) {
	repo := newFakeAutomationRepo()
	reloads := 0
	mod := NewModule(repo, WithWorkflowsChanged(func() { reloads++ }))
	ctx := context.Background()
	dana := workflowCaller(t, "levi", "dana")

	res, err := mod.Execute(ctx, dana, "schedule_workflow", map[string]interface{}{
		"name": "Morning summary", "cron_expression": "0 7 * * *", "instruction": "Summarise my day",
	})
	require.NoError(t, err)
	assert.Equal(t, "✅ **Workflow Scheduled!**\n\n'Morning summary' will run on schedule: 0 7 * * *", res)
	assert.Equal(t, 1, reloads, "the scheduler reloads at once")

	rules, err := repo.ListAutomationRules(dana.Scope, "")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	r := rules[0]
	assert.Equal(t, "dana", r.UserID)
	assert.Equal(t, dana.Scope.HouseholdID(), r.HouseholdID)
	assert.Equal(t, "cron", r.TriggerType)
	assert.JSONEq(t, `{"cron": "0 7 * * *"}`, r.ConditionJSON)
	assert.True(t, r.IsActive)
	instruction, ok := PromptOf(r)
	require.True(t, ok, "the action is a prompt")
	assert.Equal(t, "Summarise my day", instruction)
	assert.True(t, NotifiesPerson(r), "its answer reaches its person, so quiet hours hold it")

	// An expression the scheduler could not run is refused, and nothing is stored.
	res, err = mod.Execute(ctx, dana, "schedule_workflow", map[string]interface{}{
		"name": "Broken", "cron_expression": "every morning", "instruction": "x",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Error scheduling workflow")
	rules, _ = repo.ListAutomationRules(dana.Scope, "")
	assert.Len(t, rules, 1)
}

// list_workflows shows the caller's own active workflows, in the fields of the old workflows table:
// not another member's, not an ordinary rule, not an inactive one.
func TestListWorkflows_ShowsOnlyTheCallersOwnActiveWorkflows(t *testing.T) {
	repo := newFakeAutomationRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	dana, omer := workflowCaller(t, "levi", "dana"), workflowCaller(t, "levi", "omer")

	for _, c := range []struct {
		caller orchestrator.CallContext
		name   string
	}{{dana, "Dana's"}, {omer, "Omer's"}} {
		_, err := mod.Execute(ctx, c.caller, "schedule_workflow", map[string]interface{}{
			"name": c.name, "cron_expression": "0 8 * * *", "instruction": "Plan " + c.name,
		})
		require.NoError(t, err)
	}
	require.NoError(t, repo.CreateAutomationRule(dana.Scope, &models.AutomationRule{UserID: "dana", Name: "Plain rule",
		TriggerType: "cron", ConditionJSON: `{"cron": "0 9 * * *"}`, ActionInstruction: "notify: hi", IsActive: true}))
	paused := &models.AutomationRule{UserID: "dana", Name: "Paused", TriggerType: "cron",
		ConditionJSON: `{"cron": "0 10 * * *"}`, ActionInstruction: "prompt: later", IsActive: false}
	require.NoError(t, repo.CreateAutomationRule(dana.Scope, paused))

	res, err := mod.Execute(ctx, dana, "list_workflows", nil)
	require.NoError(t, err)
	require.Contains(t, res, "Here are your active workflows:\n")
	var listed []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res[len("Here are your active workflows:\n"):]), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "Dana's", listed[0]["name"])
	assert.Equal(t, "0 8 * * *", listed[0]["cron_expression"])
	assert.Equal(t, "Plan Dana's", listed[0]["instruction"])
	assert.Equal(t, "dana", listed[0]["user_id"])

	res, err = mod.Execute(ctx, workflowCaller(t, "levi", "noa"), "list_workflows", nil)
	require.NoError(t, err)
	assert.Equal(t, "You have no active workflows scheduled.", res)
}

// delete_workflow deletes only the caller's own workflow: another member's, or a rule that is not
// a workflow, stays.
func TestDeleteWorkflow_DeletesOnlyTheCallersOwnWorkflow(t *testing.T) {
	repo := newFakeAutomationRepo()
	reloads := 0
	mod := NewModule(repo, WithWorkflowsChanged(func() { reloads++ }))
	ctx := context.Background()
	dana, omer := workflowCaller(t, "levi", "dana"), workflowCaller(t, "levi", "omer")

	_, err := mod.Execute(ctx, dana, "schedule_workflow", map[string]interface{}{
		"name": "Mine", "cron_expression": "0 8 * * *", "instruction": "x",
	})
	require.NoError(t, err)
	rules, _ := repo.ListAutomationRules(dana.Scope, "")
	require.Len(t, rules, 1)
	mine := rules[0].ID
	plain := &models.AutomationRule{UserID: "dana", Name: "Plain", TriggerType: "cron",
		ConditionJSON: `{"cron": "0 9 * * *"}`, ActionInstruction: "notify: hi", IsActive: true}
	require.NoError(t, repo.CreateAutomationRule(dana.Scope, plain))

	res, err := mod.Execute(ctx, omer, "delete_workflow", map[string]interface{}{"id": fmt.Sprint(mine)})
	require.NoError(t, err)
	assert.Contains(t, res, "you have no workflow with ID")
	res, err = mod.Execute(ctx, dana, "delete_workflow", map[string]interface{}{"id": fmt.Sprint(plain.ID)})
	require.NoError(t, err)
	assert.Contains(t, res, "you have no workflow with ID", "a rule that is not a workflow is not deleted by this tool")
	rules, _ = repo.ListAutomationRules(dana.Scope, "")
	assert.Len(t, rules, 2)

	reloads = 0
	bus := events.NewEventBus()
	var published []events.MutationEvent
	bus.SubscribeSync(func(_ context.Context, ev events.MutationEvent) error {
		published = append(published, ev)
		return nil
	})
	dana.EventBus = bus
	res, err = mod.Execute(ctx, dana, "delete_workflow", map[string]interface{}{"id": fmt.Sprint(mine)})
	require.NoError(t, err)
	assert.Equal(t, "Workflow deleted successfully.", res)
	assert.Equal(t, 1, reloads)
	// The deletion carries the rule as it was, which is what /undo restores.
	require.Len(t, published, 1)
	assert.Equal(t, "delete", published[0].Action)
	assert.Equal(t, "AutomationRule", published[0].EntityType)
	var before models.AutomationRule
	require.NoError(t, json.Unmarshal([]byte(published[0].BeforeJSON), &before))
	assert.Equal(t, mine, before.ID)
	assert.Equal(t, "prompt: x", before.ActionInstruction)
	rules, _ = repo.ListAutomationRules(dana.Scope, "")
	require.Len(t, rules, 1)
	assert.Equal(t, plain.ID, rules[0].ID)
}

// PromptOf recognises only a cron rule whose action starts with the prompt prefix.
func TestPromptOf(t *testing.T) {
	for _, c := range []struct {
		trigger, action, want string
		ok                    bool
	}{
		{"cron", "prompt: Summarise my day", "Summarise my day", true},
		{"CRON", "  Prompt:plan  ", "plan", true},
		{"cron", "notify: hi", "", false},
		{"cron", "Summarise my day", "", false},
		{"event_mutation", "prompt: x", "", false},
		{"cron", "prompt", "", false},
	} {
		got, ok := PromptOf(models.AutomationRule{TriggerType: c.trigger, ActionInstruction: c.action})
		assert.Equal(t, c.ok, ok, "%q %q", c.trigger, c.action)
		assert.Equal(t, c.want, got, "%q %q", c.trigger, c.action)
	}
}
