package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-07 review: a scheduled workflow was one person's own (the workflows table, read through
// personIs). Folded into the household's automation_rules, it stays its person's own on every
// scoped path: another member, an admin included, cannot read, change, re-activate or delete it
// through the rule tools, the dashboard query or the toggle. An ordinary rule stays the household's.
func TestAutomationRules_AWorkflowIsSeenOnlyByItsPerson(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	dana := scopeAs(t, client, "levi-wf", "dana-wf", "family")
	omer := scopeAs(t, client, "levi-wf", "omer-wf", "admin")
	require.Equal(t, dana.HouseholdID(), omer.HouseholdID())

	workflow := &models.AutomationRule{UserID: "dana-wf", Name: "Morning summary", TriggerType: "cron",
		TriggerTopic: "*", ConditionJSON: `{"cron": "0 7 * * *"}`, ActionInstruction: "prompt: Summarise my day", IsActive: true}
	require.NoError(t, client.CreateAutomationRule(dana, workflow))
	shared := &models.AutomationRule{UserID: "dana-wf", Name: "Budget alert", TriggerType: "cron",
		TriggerTopic: "*", ConditionJSON: `{"cron": "0 9 * * *"}`, ActionInstruction: "notify: check the budget", IsActive: true}
	require.NoError(t, client.CreateAutomationRule(dana, shared))

	names := func(rules []models.AutomationRule) []string {
		var out []string
		for _, r := range rules {
			out = append(out, r.Name)
		}
		return out
	}
	mine, err := client.ListAutomationRules(dana, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Morning summary", "Budget alert"}, names(mine), "its person sees the workflow")
	theirs, err := client.ListAutomationRules(omer, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Budget alert"}, names(theirs), "another member sees only the household's rules")

	got, err := client.GetAutomationRule(omer, workflow.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "another member cannot read it")
	off := *workflow
	off.IsActive = false
	assert.Error(t, client.UpdateAutomationRule(omer, &off), "another member cannot change or toggle it")
	require.NoError(t, client.DeleteAutomationRule(omer, workflow.ID))
	got, err = client.GetAutomationRule(dana, workflow.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "another member's delete leaves it")
	assert.True(t, got.IsActive)

	// The ordinary rule is the household's, as before.
	got, err = client.GetAutomationRule(omer, shared.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Its person can change and delete it; the deletion is undone from its snapshot (delete_workflow
	// publishes the rule as before_json).
	require.NoError(t, client.UpdateAutomationRule(dana, &off))
	before, err := json.Marshal(off)
	require.NoError(t, err)
	require.NoError(t, client.DeleteAutomationRule(dana, workflow.ID))
	got, err = client.GetAutomationRule(dana, workflow.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
	log := models.MutationLog{ID: uuid.NewString(), UserID: "dana-wf", Topic: "automation", Action: "delete",
		EntityType: "AutomationRule", EntityID: "1", BeforeJSON: string(before), Summary: "Deleted workflow"}
	require.NoError(t, client.InsertMutationLog(dana, log))
	require.NoError(t, client.RollbackSnapshot(context.Background(), dana, log, "automation_rules"))
	got, err = client.GetAutomationRule(dana, workflow.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "undo restores the deleted workflow")
	assert.Equal(t, "prompt: Summarise my day", got.ActionInstruction)
	got, err = client.GetAutomationRule(omer, workflow.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "and it is still its person's own")
}
