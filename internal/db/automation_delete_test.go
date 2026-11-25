package db

import (
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-39: deleting an automation rule that matched no row is not-found, like every other delete by
// ID: a rule of another household, and a missing one. The other household's rule stays.
func TestDeleteAutomationRule_NoRowIsNotFound(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	a := scopeAs(t, c, "fam_a", "user_a", "family")
	b := scopeAs(t, c, "fam_b", "user_b", "family")
	rule := models.AutomationRule{UserID: "user_a", Name: "Budget alert", TriggerType: "event", TriggerTopic: "finance",
		ConditionJSON: "{}", ActionInstruction: "notify: over budget", IsActive: true}
	require.NoError(t, c.CreateAutomationRule(a, &rule))

	err := c.DeleteAutomationRule(b, rule.ID)
	require.Error(t, err, "a rule of another household is not found")
	assert.Contains(t, err.Error(), "not found")
	err = c.DeleteAutomationRule(a, rule.ID+1000)
	require.Error(t, err, "a missing rule is not found")
	assert.Contains(t, err.Error(), "not found")

	still, err := c.GetAutomationRule(a, rule.ID)
	require.NoError(t, err)
	require.NotNil(t, still, "the other household's delete left it")
	require.NoError(t, c.DeleteAutomationRule(a, rule.ID))
}
