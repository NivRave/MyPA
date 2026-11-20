package scheduler

import (
	"context"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/packs"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil"
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
	// The workflow's person, in the rule's household (with every pack on): since V4-27 a workflow
	// runs only for a person whose household has the Automations pack.
	householdtest.User(t, client, "test-scheduler-user", "Scheduler", "family", "fam-test")

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
		HouseholdID:       householdtest.Household(t, client, "fam-test").ID, // V4-30
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
		HouseholdID:       householdtest.Household(t, client, "fam-test").ID, // V4-30
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

// V4-27: workflows and cron rules belong to the Automations pack. A household that switched it off
// gets none of them scheduled; its rows are kept. Another household's are unaffected.
func TestReloadWorkflows_SkipsHouseholdsWithAutomationsOff(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	add := func(label, person string) {
		u := householdtest.User(t, client, person, person, "family", label)
		require.NoError(t, client.SaveWorkflow(models.Workflow{UserID: person, Name: "wf-" + person,
			CronExpression: "0 8 * * *", Instruction: "Check", IsActive: true, CreatedAt: time.Now()}))
		require.NoError(t, client.DB.Create(&models.AutomationRule{UserID: person, HouseholdID: u.HouseholdID,
			FamilyGroup: label, Name: "rule-" + person, TriggerType: "cron", ConditionJSON: `{"cron": "0 9 * * 1"}`,
			ActionInstruction: "notify: hi", IsActive: true, CreatedAt: time.Now()}).Error)
	}
	add("fam-on", "person-on")
	add("fam-off", "person-off")
	off, err := identity.ForStoredRow(ctx, client, householdtest.Household(t, client, "fam-off").ID, "person-off")
	require.NoError(t, err)
	_, err = client.SetHouseholdPacks(ctx, off, packs.Everything().With(packs.Automations, false))
	require.NoError(t, err)

	ReloadWorkflows(nil, client, automation.NewRunner(client, nil))
	workflowMu.Lock()
	entries := len(workflowCron.Entries())
	workflowMu.Unlock()
	assert.Equal(t, 2, entries, "only the household with Automations on gets its workflow and its rule")

	var kept int64
	require.NoError(t, client.DB.Model(&models.Workflow{}).Where("user_id = ?", "person-off").Count(&kept).Error)
	assert.Equal(t, int64(1), kept, "the switched-off household's workflow is kept")
}
