package automation

import (
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestNotifiesPerson(t *testing.T) {
	assert.True(t, NotifiesPerson(models.AutomationRule{ActionInstruction: "notify: take the bins out"}))
	assert.True(t, NotifiesPerson(models.AutomationRule{ActionInstruction: "  NOTIFY: bins"}))
	assert.False(t, NotifiesPerson(models.AutomationRule{ActionInstruction: "https://example.com/hook"}))
	assert.False(t, NotifiesPerson(models.AutomationRule{ActionInstruction: "webhook: https://example.com/hook"}))
	assert.False(t, NotifiesPerson(models.AutomationRule{}))
}

func TestCronRuleUrgent(t *testing.T) {
	assert.True(t, CronRuleUrgent(`{"cron": "0 23 * * *", "urgent": true}`))
	assert.False(t, CronRuleUrgent(`{"cron": "0 23 * * *", "urgent": false}`))
	assert.False(t, CronRuleUrgent(`{"cron": "0 23 * * *"}`))
	assert.False(t, CronRuleUrgent(`0 23 * * *`))
	assert.False(t, CronRuleUrgent(`{"urgent": "yes"}`), "only a JSON true counts")
	assert.False(t, CronRuleUrgent(`{not json`))
}
