package scheduler

import (
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadWorkflows_NilDB(t *testing.T) {
	assert.NotPanics(t, func() {
		ReloadWorkflows(nil, nil, nil)
	})

	workflowMu.Lock()
	assert.NotNil(t, workflowCron)
	entries := workflowCron.Entries()
	workflowMu.Unlock()
	assert.Empty(t, entries)
}

func TestReloadWorkflows_ConcurrentSafety(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ReloadWorkflows(nil, nil, nil)
		}()
	}
	wg.Wait()

	workflowMu.Lock()
	assert.NotNil(t, workflowCron)
	workflowMu.Unlock()
}

func TestReloadWorkflows_WithPostgres(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	// Clean up previous test entries
	_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.Workflow{})
	_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.AutomationRule{})

	defer func() {
		_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.Workflow{})
		_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.AutomationRule{})
	}()

	runner := automation.NewRunner(client, nil)

	// 1. Insert active legacy workflow
	err := client.SaveWorkflow(models.Workflow{
		UserID:         "test-scheduler-user",
		Name:           "Test Legacy Workflow",
		CronExpression: "0 8 * * *",
		Instruction:    "Check pending items",
		IsActive:       true,
		CreatedAt:      time.Now(),
	})
	require.NoError(t, err)

	// 2. Insert active cron automation rule
	cronRule := models.AutomationRule{
		UserID:            "test-scheduler-user",
		FamilyGroup:       "fam-test",
		Name:              "Test Cron Rule",
		TriggerType:       "cron",
		TriggerTopic:      "automation",
		ConditionJSON:     `{"cron": "0 9 * * 1"}`,
		ActionInstruction: "notify: Weekly check",
		IsActive:          true,
		CreatedAt:         time.Now(),
	}
	err = client.DB.Create(&cronRule).Error
	require.NoError(t, err)

	// 3. Insert inactive cron rule (must not be scheduled)
	inactiveRule := models.AutomationRule{
		UserID:            "test-scheduler-user",
		FamilyGroup:       "fam-test",
		Name:              "Inactive Rule",
		TriggerType:       "cron",
		TriggerTopic:      "automation",
		ConditionJSON:     `{"cron": "0 10 * * *"}`,
		ActionInstruction: "notify: should not run",
		IsActive:          false,
		CreatedAt:         time.Now(),
	}
	err = client.DB.Create(&inactiveRule).Error
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		ReloadWorkflows(nil, client, runner)
	})

	workflowMu.Lock()
	require.NotNil(t, workflowCron)
	entries := workflowCron.Entries()
	workflowMu.Unlock()

	// Should have at least 2 entries
	assert.GreaterOrEqual(t, len(entries), 2)
}
