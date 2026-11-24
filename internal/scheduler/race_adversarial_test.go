package scheduler_test

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/scheduler"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	return client
}

// TestAdversarial_ConcurrencyDataRaceStress stress-tests scheduler.ReloadWorkflows
// under Go's race detector (-race) to ensure workflowMu strictly protects workflowCron
// from concurrent read/write data races or panics.
func TestAdversarial_ConcurrencyDataRaceStress(t *testing.T) {
	client := newTestDBClient(t)

	testUser := "adv_race_user"
	testFam := "adv_race_fam"

	// Cleanup
	defer func() {
		client.DB.Where("user_id = ?", testUser).Delete(&models.AutomationRule{})
	}()
	client.DB.Where("user_id = ?", testUser).Delete(&models.AutomationRule{})

	// Insert initial scheduled workflows (cron rules whose action is a prompt, V4-07) and cron rules
	for i := 1; i <= 5; i++ {
		wf := models.AutomationRule{
			UserID:            testUser,
			HouseholdID:       householdtest.Household(t, client, testFam).ID,
			FamilyGroup:       testFam,
			Name:              fmt.Sprintf("Race Workflow %d", i),
			TriggerType:       "cron",
			TriggerTopic:      "*",
			ConditionJSON:     `{"cron": "0 8 * * *"}`,
			ActionInstruction: automation.PromptPrefix + " Do work",
			IsActive:          true,
			CreatedAt:         time.Now(),
		}
		require.NoError(t, client.DB.Create(&wf).Error)

		rule := models.AutomationRule{
			UserID:            testUser,
			HouseholdID:       householdtest.Household(t, client, testFam).ID, // V4-30
			FamilyGroup:       testFam,
			Name:              fmt.Sprintf("Race Rule %d", i),
			TriggerType:       "cron",
			TriggerTopic:      "automation",
			ConditionJSON:     fmt.Sprintf(`{"cron": "0 %d * * *"}`, (i%23)+1),
			ActionInstruction: "notify: Race action",
			IsActive:          true,
			CreatedAt:         time.Now(),
		}
		require.NoError(t, client.DB.Create(&rule).Error)
	}

	runner := automation.NewRunner(client, nil)

	// Concurrency test: 50 concurrent workers calling ReloadWorkflows
	// 50 concurrent workers reading/triggering workflows
	// Run for several iterations under -race
	const numWorkers = 50
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errChan := make(chan error, numWorkers*2)

	for i := 0; i < numWorkers; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Call ReloadWorkflows
					assert.NotPanics(t, func() {
						scheduler.ReloadWorkflows(nil, client, runner)
					})
					// Add small random jitter
					time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
				}
			}
		}()

		// Concurrent database updates toggling rules
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					ruleID := (workerID % 5) + 1
					_ = client.DB.Model(&models.AutomationRule{}).
						Where("user_id = ? AND name = ?", testUser, fmt.Sprintf("Race Rule %d", ruleID)).
						Update("is_active", rand.Intn(2) == 1)
					time.Sleep(time.Duration(rand.Intn(15)) * time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		require.NoError(t, err)
	}
}
