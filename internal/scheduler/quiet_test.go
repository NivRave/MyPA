package scheduler

import (
	"testing"
	"time"

	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/quiet"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

// counted is a job that records how often it ran.
func counted(t *testing.T, name, userID, expr string, loc *time.Location, holdable bool, runs map[string]int) job {
	t.Helper()
	sched, err := scheduleIn(expr, loc)
	require.NoError(t, err)
	return job{name: name, userID: userID, sched: sched, holdable: holdable, run: func() { runs[name]++ }}
}

// V4-05: an automation that reaches a person and fires in their quiet hours is held, and runs
// once when they end; one that reaches no one, or is urgent, runs on time.
func TestAutomations_HeldInQuietHoursRunOnceWhenTheyEnd(t *testing.T) {
	jerusalem := mustZone(t, "Asia/Jerusalem")
	clockOf := func(string) quiet.Clock {
		return quiet.NewClock(quiet.Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, "UTC")
	}
	runs := map[string]int{}
	jobs := []job{
		counted(t, "nightly notify", "dana", "0 23 * * *", jerusalem, true, runs),
		counted(t, "every 30 minutes", "dana", "*/30 * * * *", jerusalem, true, runs),
		counted(t, "webhook", "dana", "0 23 * * *", jerusalem, false, runs),
		counted(t, "noon notify", "dana", "0 12 * * *", jerusalem, true, runs),
		counted(t, "at the end", "dana", "0 7 * * *", jerusalem, true, runs),
	}

	at := time.Date(2026, 11, 20, 23, 0, 0, 0, jerusalem)
	for _, j := range jobs[:3] {
		j.fire(clockOf, at)
	}
	assert.Zero(t, runs["nightly notify"], "a message at 23:00 waits")
	assert.Zero(t, runs["every 30 minutes"])
	assert.Equal(t, 1, runs["webhook"], "a webhook reaches no one and runs on time")

	// Released at 07:00, once, whatever the number of fires it missed; a job that had no fire
	// in the window is not run, and neither is one whose own time is 07:00 (it fires normally).
	assert.Empty(t, releaseHeld(jobs, clockOf, time.Date(2026, 11, 21, 6, 59, 0, 0, jerusalem)))
	ran := releaseHeld(jobs, clockOf, time.Date(2026, 11, 21, 7, 0, 0, 0, jerusalem))
	assert.ElementsMatch(t, []string{"nightly notify", "every 30 minutes"}, ran)
	assert.Equal(t, 1, runs["nightly notify"])
	assert.Equal(t, 1, runs["every 30 minutes"])
	assert.Zero(t, runs["noon notify"])
	assert.Zero(t, runs["at the end"])
	assert.Empty(t, releaseHeld(jobs, clockOf, time.Date(2026, 11, 21, 7, 1, 0, 0, jerusalem)))

	jobs[4].fire(clockOf, time.Date(2026, 11, 21, 7, 0, 0, 0, jerusalem))
	assert.Equal(t, 1, runs["at the end"], "07:00 is outside quiet hours")

	// Someone with no quiet hours is never held.
	none := func(string) quiet.Clock { return quiet.NewClock(quiet.Settings{TimeZone: "Asia/Jerusalem"}, "UTC") }
	jobs[0].fire(none, at)
	assert.Equal(t, 2, runs["nightly notify"])
}

// A rule's cron expression is read on its person's clock: their household's time zone, else the
// install's. An expression that names its own zone keeps it. Before V4-05 every rule ran in the
// install's zone.
func TestReloadWorkflows_SchedulesEachRuleOnItsPersonsClock(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	rule := func(person, label, zone, condition, action string) {
		u := householdtest.User(t, client, person, person, "admin", label)
		require.NoError(t, client.DB.Model(&models.Household{}).Where("id = ?", u.HouseholdID).Update("time_zone", zone).Error)
		require.NoError(t, client.DB.Create(&models.AutomationRule{UserID: person, HouseholdID: u.HouseholdID,
			FamilyGroup: label, Name: person, TriggerType: "cron", ConditionJSON: condition,
			ActionInstruction: action, IsActive: true, CreatedAt: time.Now()}).Error)
	}
	rule("tz-ny", "TzNewYork", "America/New_York", `{"cron": "0 9 * * *"}`, "notify: stand-up")
	rule("tz-install", "TzInstall", "", `{"cron": "0 9 * * *"}`, "https://example.com/hook")
	rule("tz-own", "TzOwn", "America/New_York", `{"cron": "CRON_TZ=Asia/Tokyo 0 9 * * *", "urgent": true}`, "notify: own zone")
	require.NoError(t, client.SaveWorkflow(models.Workflow{UserID: "tz-ny", Name: "wf", CronExpression: "30 18 * * *",
		Instruction: "Summarise my day", IsActive: true, CreatedAt: time.Now()}))

	ReloadWorkflows(nil, client, automation.NewRunner(client, nil), "Asia/Jerusalem")
	workflowMu.Lock()
	jobs := append([]job(nil), currentJobs...)
	workflowMu.Unlock()
	byName := map[string]job{}
	for _, j := range jobs {
		byName[j.name] = j
	}
	require.Len(t, byName, 4)

	from := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC) // 15:00 in New York, 05:00 next day in Tokyo
	next := func(name string) time.Time {
		j, ok := byName[name]
		require.True(t, ok, name)
		return j.sched.Next(from)
	}
	ny, jerusalem, tokyo := mustZone(t, "America/New_York"), mustZone(t, "Asia/Jerusalem"), mustZone(t, "Asia/Tokyo")
	ruleID := func(person string) string {
		var r models.AutomationRule
		require.NoError(t, client.DB.Where("user_id = ?", person).First(&r).Error)
		return ruleName(r)
	}
	assert.True(t, next(ruleID("tz-ny")).Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, ny)), "09:00 in New York")
	assert.True(t, next(ruleID("tz-install")).Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, jerusalem)), "a household without a zone uses the install's")
	assert.True(t, next(ruleID("tz-own")).Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, tokyo)), "CRON_TZ in the expression wins")

	var wf models.Workflow
	require.NoError(t, client.DB.Where("user_id = ?", "tz-ny").First(&wf).Error)
	assert.True(t, next(workflowName(wf)).Equal(time.Date(2026, 11, 19, 18, 30, 0, 0, ny)), "a workflow follows its person's zone too")

	assert.True(t, byName[ruleID("tz-ny")].holdable, "a notify rule is held in quiet hours")
	assert.False(t, byName[ruleID("tz-install")].holdable, "a webhook rule is not")
	assert.False(t, byName[ruleID("tz-own")].holdable, "an urgent rule is not")
	assert.True(t, byName[workflowName(wf)].holdable, "a workflow's answer reaches its person")

	// The workflow cron holds exactly these jobs; the release check runs on the main cron.
	workflowMu.Lock()
	entries := len(workflowCron.Entries())
	workflowMu.Unlock()
	assert.Equal(t, 4, entries)
}
