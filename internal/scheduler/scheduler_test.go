package scheduler

import (
	"context"
	"fmt"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/orchestrator"
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
	_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.AutomationRule{})

	defer func() {
		_ = client.DB.Where("user_id = ?", "test-scheduler-user").Delete(&models.AutomationRule{})
	}()

	runner := automation.NewRunner(client, nil)
	// The workflow's person, in the rule's household (with every pack on): since V4-27 a workflow
	// runs only for a person whose household has the Automations pack.
	householdtest.User(t, client, "test-scheduler-user", "Scheduler", "family", "fam-test")

	// 1. A scheduled workflow: a cron rule whose action is a prompt (V4-07)
	workflowRule(t, client, "test-scheduler-user", "fam-test", "Test Workflow", "0 8 * * *", "Check pending items")

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
	err := client.DB.Create(&cronRule).Error
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
	// The column's default (true) replaces a false IsActive on create (BL-62): switch it off.
	require.NoError(t, client.DB.Model(&inactiveRule).Update("is_active", false).Error)

	assert.NotPanics(t, func() {
		ReloadWorkflows(nil, client, runner)
	})

	workflowMu.Lock()
	require.NotNil(t, workflowCron)
	entries := workflowCron.Entries()
	workflowMu.Unlock()

	// The workflow and the active rule; not the inactive one
	assert.Len(t, entries, 2)
}

// V4-27: workflows and cron rules belong to the Automations pack. A household that switched it off
// gets none of them scheduled; its rows are kept. Another household's are unaffected.
func TestReloadWorkflows_SkipsHouseholdsWithAutomationsOff(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	add := func(label, person string) {
		u := householdtest.User(t, client, person, person, "family", label)
		workflowRule(t, client, person, label, "wf-"+person, "0 8 * * *", "Check")
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
	require.NoError(t, client.DB.Model(&models.AutomationRule{}).Where("user_id = ? AND name = ?", "person-off", "wf-person-off").Count(&kept).Error)
	assert.Equal(t, int64(1), kept, "the switched-off household's workflow is kept")
}

// workflowRule stores a scheduled workflow of a person: since V4-07 a cron rule of their household
// whose action is a prompt, as the workflow tools and migration 11 write it.
func workflowRule(t *testing.T, client *db.Client, person, label, name, cronExpr, instruction string) models.AutomationRule {
	t.Helper()
	u, err := client.GetUser(person)
	if err != nil || u == nil {
		created := householdtest.User(t, client, person, person, "family", label)
		u = &created
	}
	r := models.AutomationRule{UserID: person, HouseholdID: u.HouseholdID, FamilyGroup: label, Name: name,
		TriggerType: "cron", TriggerTopic: "*", ConditionJSON: `{"cron": "` + cronExpr + `"}`,
		ActionInstruction: automation.PromptPrefix + " " + instruction, IsActive: true, CreatedAt: time.Now()}
	require.NoError(t, client.DB.Create(&r).Error)
	return r
}

// V4-07: the legacy workflow loader is folded into the cron rules. A workflow's rule runs its
// instruction as a turn for its person (as the loader ran the workflow), never through the
// runner's actions, and not once its household has switched the Automations pack off.
func TestReloadWorkflows_AWorkflowRuleRunsItsInstructionAsATurn(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	type turn struct{ userID, instruction, msgID string }
	var mu sync.Mutex
	var turns []turn
	saved := startTurn
	startTurn = func(_ *orchestrator.Engine, _ *db.Client, userID, instruction, msgID string) {
		mu.Lock()
		defer mu.Unlock()
		turns = append(turns, turn{userID, instruction, msgID})
	}
	t.Cleanup(func() { startTurn = saved })
	var actions int
	runner := automation.NewRunner(client, nil, automation.WithActionExecutor(func(context.Context, models.AutomationRule, events.MutationEvent) error {
		actions++
		return nil
	}))

	wf := workflowRule(t, client, "wf-person", "fam-wf", "Morning summary", "0 7 * * *", "Summarise my day")
	ReloadWorkflows(nil, client, runner, "Asia/Jerusalem")
	workflowMu.Lock()
	jobs := append([]job(nil), currentJobs...)
	workflowMu.Unlock()
	require.Len(t, jobs, 1)
	assert.Equal(t, ruleName(wf), jobs[0].name)
	assert.Equal(t, "wf-person", jobs[0].userID, "it follows its person's clock")
	assert.True(t, jobs[0].holdable, "its answer reaches its person: held in quiet hours")

	jobs[0].run()
	require.Len(t, turns, 1)
	assert.Equal(t, "wf-person", turns[0].userID)
	assert.Equal(t, "Summarise my day", turns[0].instruction, "the instruction without the prefix")
	assert.Contains(t, turns[0].msgID, fmt.Sprintf("rule-cron-%d-", wf.ID))
	assert.Zero(t, actions, "a workflow is a turn, not a runner action")

	// Switched off after the reload: the next fire starts nothing.
	off, err := identity.ForStoredRow(ctx, client, wf.HouseholdID, "wf-person")
	require.NoError(t, err)
	_, err = client.SetHouseholdPacks(ctx, off, packs.Everything().With(packs.Automations, false))
	require.NoError(t, err)
	jobs[0].run()
	assert.Len(t, turns, 1, "no turn once the household has the Automations pack off")
}
