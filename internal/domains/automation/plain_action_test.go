package automation

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-63: a plain instruction on a new cron rule is stored as a prompt: action; one on a rule
// triggered by an event would do nothing, so it is refused when the rule is created. Webhooks,
// notify: and an explicit prompt: on a cron rule are stored as given. The tool says so.
func TestCreateRule_APlainInstructionIsAPromptOnASchedule(t *testing.T) {
	repo := newFakeAutomationRepo()
	mod := NewModule(repo)
	callCtx := orchestrator.CallContext{
		User:  &models.User{PlatformID: "u_bl63"},
		Scope: identitytest.Scope(t, "bl63", "u_bl63", "family"),
	}
	ctx := context.Background()
	create := func(name, trigger, action string) (string, error) {
		cond := `{}`
		if trigger == "cron" {
			cond = `{"cron": "0 9 * * *"}`
		}
		return mod.Execute(ctx, callCtx, "create_automation_rule", map[string]interface{}{
			"name": name, "trigger_type": trigger, "trigger_topic": "*", "condition_json": cond, "action_instruction": action})
	}
	stored := func(name string) string {
		for _, r := range repo.rules {
			if r.Name == name {
				return r.ActionInstruction
			}
		}
		return "<missing>"
	}

	_, err := create("plain", "cron", "Summarise my day")
	require.NoError(t, err)
	assert.Equal(t, "prompt: Summarise my day", stored("plain"))

	for name, action := range map[string]string{
		"prompt":  "prompt: Check the weather",
		"notify":  "notify: Water the plants",
		"webhook": "https://example.com/hook",
	} {
		_, err := create(name, "cron", action)
		require.NoError(t, err)
		assert.Equal(t, action, stored(name), name)
	}

	for _, action := range []string{"Tell me when Dana spends", "prompt: Tell me"} {
		res, err := create("event "+action, "event_mutation", action)
		require.NoError(t, err)
		assert.Contains(t, res, "notify:", "the answer says what an event rule can do")
		assert.Equal(t, "<missing>", stored("event "+action), "an event rule whose action would do nothing is not created")
	}
	_, err = create("event notify", "event_mutation", "notify: Dana spent")
	require.NoError(t, err)
	assert.Equal(t, "notify: Dana spent", stored("event notify"))

	var desc string
	for _, d := range mod.Declarations() {
		if d.Name == "create_automation_rule" {
			desc = d.Parameters.Properties["action_instruction"].Description
		}
	}
	assert.True(t, strings.Contains(desc, "prompt:"), "the tool describes the prompt: action: %s", desc)
}
