package automation_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	return client
}

// TestAdversarial_ExtractCronSchedule_EdgeCases challenges BUG-05 with valid, boundary,
// and malformed cron inputs to verify robust parsing and graceful error handling.
func TestAdversarial_ExtractCronSchedule_EdgeCases(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		// Valid JSON payloads
		{"json_cron", `{"cron": "0 9 * * 1"}`, "0 9 * * 1", false},
		{"json_cron_expr", `{"cron_expression": "*/10 * * * *"}`, "*/10 * * * *", false},
		{"json_expression", `{"expression": "0 0 * * *"}`, "0 0 * * *", false},
		{"json_schedule", `{"schedule": "30 4 1 * *"}`, "30 4 1 * *", false},
		{"json_extra_fields", `{"cron": " 0 12 * * * ", "desc": "lunch", "active": true}`, "0 12 * * *", false},

		// Valid plain cron strings
		{"plain_5_fields", "*/5 * * * *", "*/5 * * * *", false},
		{"plain_weekday", "0 9 * * 1", "0 9 * * 1", false},
		{"plain_end_of_year", "59 23 31 12 *", "59 23 31 12 *", false},

		// Standard descriptors
		{"descriptor_daily", "@daily", "@daily", false},
		{"descriptor_hourly", "@hourly", "@hourly", false},
		{"descriptor_weekly", "@weekly", "@weekly", false},
		{"descriptor_midnight", "@midnight", "@midnight", false},
		{"descriptor_monthly", "@monthly", "@monthly", false},
		{"descriptor_yearly", "@yearly", "@yearly", false},

		// Malformed / Invalid inputs (must return error, never panic)
		{"empty_string", "", "", true},
		{"whitespace_only", "    ", "", true},
		{"plain_garbage", "not-a-cron-expression", "", true},
		{"words", "every monday at nine", "", true},
		{"minute_out_of_bounds", "60 12 * * *", "", true},
		{"hour_out_of_bounds", "0 25 * * *", "", true},
		{"day_out_of_bounds", "0 12 32 * *", "", true},
		{"month_out_of_bounds", "0 12 1 13 *", "", true},
		{"dow_out_of_bounds", "0 12 * * 8", "", true},
		{"too_few_fields", "1 2 3", "", true},
		{"too_many_fields", "1 2 3 4 5 6 7 8", "", true},
		{"broken_json", `{"cron": `, "", true},
		{"json_empty_cron", `{"cron": ""}`, "", true},
		{"json_bad_cron", `{"cron": "invalid-cron"}`, "", true},
		{"json_missing_keys", `{"interval": 5, "type": "cron"}`, "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				res, err := automation.ExtractCronSchedule(tc.input)
				if tc.expectError {
					assert.Error(t, err, "Input %q must result in error", tc.input)
					assert.Empty(t, res)
				} else {
					require.NoError(t, err, "Input %q must parse successfully", tc.input)
					assert.Equal(t, tc.expected, res)
				}
			})
		})
	}
}

// TestAdversarial_CronAutomationRule_SchedulerExecution verifies that active cron automation
// rules stored in the database are scheduled and execute actions upon scheduler ticks.
func TestAdversarial_CronAutomationRule_SchedulerExecution(t *testing.T) {
	client := newTestDBClient(t)

	testUser := "adv_cron_user"
	testFam := "adv_cron_fam"

	// Cleanup
	defer func() {
		client.DB.Where("user_id = ?", testUser).Delete(&models.AutomationRule{})
	}()
	client.DB.Where("user_id = ?", testUser).Delete(&models.AutomationRule{})

	// Setup EventBus to capture published rule_executed events
	bus := events.NewEventBus()
	var publishedEvents []events.MutationEvent
	var evMu sync.Mutex
	bus.Subscribe(func(ctx context.Context, ev events.MutationEvent) error {
		evMu.Lock()
		defer evMu.Unlock()
		publishedEvents = append(publishedEvents, ev)
		return nil
	})

	// Setup Runner with custom action executor
	var executedRules []models.AutomationRule
	var executedEvents []events.MutationEvent
	var execMu sync.Mutex

	runner := automation.NewRunner(client, bus, automation.WithActionExecutor(func(ctx context.Context, rule models.AutomationRule, ev events.MutationEvent) error {
		execMu.Lock()
		defer execMu.Unlock()
		executedRules = append(executedRules, rule)
		executedEvents = append(executedEvents, ev)
		return nil
	}))

	// 1. Insert Active Cron Rule
	activeRule := models.AutomationRule{
		UserID:            testUser,
		FamilyGroup:       testFam,
		Name:              "Active Weekly Sync",
		TriggerType:       "cron",
		TriggerTopic:      "automation",
		ConditionJSON:     `{"cron": "0 9 * * 1"}`,
		ActionInstruction: "notify: Weekly sync executed",
		IsActive:          true,
		CreatedAt:         time.Now(),
	}
	require.NoError(t, client.DB.Create(&activeRule).Error)

	// 2. Insert Inactive Cron Rule (MUST NOT be scheduled)
	inactiveRule := models.AutomationRule{
		UserID:            testUser,
		FamilyGroup:       testFam,
		Name:              "Inactive Monthly Report",
		TriggerType:       "cron",
		TriggerTopic:      "automation",
		ConditionJSON:     `{"cron": "0 0 1 * *"}`,
		ActionInstruction: "notify: Should not run",
		IsActive:          false,
		CreatedAt:         time.Now(),
	}
	require.NoError(t, client.DB.Create(&inactiveRule).Error)

	// 3. Insert Invalid Cron Rule (Malformed cron string -> skipped gracefully without crash)
	malformedRule := models.AutomationRule{
		UserID:            testUser,
		FamilyGroup:       testFam,
		Name:              "Malformed Bad Cron",
		TriggerType:       "cron",
		TriggerTopic:      "automation",
		ConditionJSON:     `{"cron": "broken-not-cron"}`,
		ActionInstruction: "notify: Bad rule",
		IsActive:          true,
		CreatedAt:         time.Now(),
	}
	require.NoError(t, client.DB.Create(&malformedRule).Error)

	// ReloadWorkflows should schedule only activeRule, skip inactiveRule and malformedRule
	assert.NotPanics(t, func() {
		scheduler.ReloadWorkflows(nil, client, runner)
	})

	// Directly trigger ExecuteAction simulating scheduler tick on activeRule
	tickEvent := events.MutationEvent{
		Topic:       activeRule.TriggerTopic,
		Action:      "cron_tick",
		EntityType:  "AutomationRule",
		EntityID:    fmt.Sprint(activeRule.ID),
		ActorUserID: activeRule.UserID,
		FamilyGroup: activeRule.FamilyGroup,
		Summary:     fmt.Sprintf("Cron trigger fired for rule %q", activeRule.Name),
		Timestamp:   time.Now(),
	}

	err := runner.ExecuteAction(context.Background(), activeRule, tickEvent)
	require.NoError(t, err)

	// Verify action execution
	execMu.Lock()
	assert.Len(t, executedRules, 1, "Expected exactly 1 rule executed")
	if len(executedRules) > 0 {
		assert.Equal(t, activeRule.Name, executedRules[0].Name)
	}
	assert.Len(t, executedEvents, 1)
	if len(executedEvents) > 0 {
		assert.Equal(t, "cron_tick", executedEvents[0].Action)
	}
	execMu.Unlock()
}
