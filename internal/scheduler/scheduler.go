package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/packs"
	"github.com/nivik/mypa/internal/quiet"
	"github.com/nivik/mypa/internal/transfer"
	"github.com/robfig/cron/v3"
)

var (
	workflowMu   sync.Mutex
	workflowCron *cron.Cron
	// currentJobs are the automations the last reload scheduled; the main cron's release check
	// reads them (V4-05). Guarded by workflowMu.
	currentJobs []job
)

// installZone is the install's DEFAULT_TIMEZONE: the argument, else the environment, else
// Asia/Jerusalem (the zone the scheduler used before it had a setting).
func installZone(defaultTimezone ...string) string {
	if len(defaultTimezone) > 0 && strings.TrimSpace(defaultTimezone[0]) != "" {
		return strings.TrimSpace(defaultTimezone[0])
	}
	if envTz := os.Getenv("DEFAULT_TIMEZONE"); envTz != "" {
		return envTz
	}
	return "Asia/Jerusalem"
}

// StartCronJobs initializes and starts background scheduling tasks. Install-wide jobs (the
// backup) run in the install's zone; everything that reaches a person runs on that person's
// clock (V4-05).
func StartCronJobs(engine *orchestrator.Engine, dbClient *db.Client, runner *automation.Runner, defaultTimezone ...string) *cron.Cron {
	tz := installZone(defaultTimezone...)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		slog.Warn("Failed to load timezone, using local time", "tz", tz, "error", err)
		loc = time.Local
	}
	c := cron.New(cron.WithLocation(loc))

	// Every day at 2:00 AM (Database Backup)
	_, _ = c.AddFunc("0 2 * * *", func() {
		slog.Info("Cron triggered: Database Backup")
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			slog.Error("DATABASE_URL is missing, cannot perform backup")
			return
		}
		backupDir := "/backups" // This will be mounted in docker-compose.yml

		opts, err := db.BackupOptionsFromEnv() // age encryption + GFS retention (P3-4)
		if err != nil {
			slog.Error("Scheduled backup skipped: invalid backup configuration", "error", err)
			return
		}
		// A backup set (V4-43): the dump plus the archive files and a manifest.
		path, err := transfer.LiveBackup(databaseURL, backupDir, opts)
		if err != nil {
			slog.Error("Scheduled backup failed", "error", err)
			return
		}
		slog.Info("Scheduled backup completed successfully", "path", path)
	})

	// Every minute: the morning briefing of each person whose briefing minute this is on their
	// own clock (08:00 in their household's zone, or the end of their quiet hours; V4-05).
	_, _ = c.AddFunc("* * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		engine.SendDueBriefings(ctx)
	})

	// Every minute, check for scheduled reminders (both personal and shared). A reminder that is
	// not urgent waits while its recipient is in quiet hours (V4-05).
	_, err = c.AddFunc("* * * * *", func() {
		engine.CheckAndSendReminders()
		// V4-33: retry what a channel refused for now, expire what was held too long.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		engine.SweepDeliveries(ctx)
	})

	// Every minute: run, once, each automation held during quiet hours that end now (V4-05). It
	// lives here rather than on the workflow cron, which a reload stops and rebuilds; quiet hours
	// often end on a minute the five-minute reload also fires.
	clockOf := clockSource(dbClient, tz)
	_, _ = c.AddFunc("* * * * *", func() {
		workflowMu.Lock()
		jobs := append([]job(nil), currentJobs...)
		workflowMu.Unlock()
		releaseHeld(jobs, memo(clockOf), time.Now())
	})

	// Every 5 minutes, reload the cron automation rules (scheduled workflows among them)
	_, _ = c.AddFunc("*/5 * * * *", func() {
		ReloadWorkflows(engine, dbClient, runner, tz)
	})

	if err != nil {
		slog.Error("failed to add cron job", "error", err)
	}

	c.Start()
	slog.Info("Cron scheduler started")

	// Schedule the cron rules immediately
	ReloadWorkflows(engine, dbClient, runner, tz)

	return c
}

// ReloadWorkflows fetches the active cron automation rules from the DB and rebuilds the workflow
// cron. A scheduled workflow is one of them since V4-07: a cron rule whose action is a prompt,
// run as a turn for its person. Concurrency-safe via workflowMu. Each runs on its person's clock
// (V4-05): the expression is read in their household's time zone, and a run that reaches them
// during their quiet hours is held until the hours end.
func ReloadWorkflows(engine *orchestrator.Engine, dbClient *db.Client, runner *automation.Runner, defaultTimezone ...string) {
	workflowMu.Lock()
	defer workflowMu.Unlock()

	if workflowCron != nil {
		workflowCron.Stop()
	}

	tz := installZone(defaultTimezone...)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	clockOf := memo(clockSource(dbClient, tz))

	workflowCron = cron.New(cron.WithLocation(loc))
	var jobs []job

	// The active cron automation rules (BUG-05). This scans every household (a scheduled job);
	// each rule runs in the scope of its own stored household_id (V4-30).
	var cronRules []models.AutomationRule
	if dbClient != nil && dbClient.DB != nil {
		if findErr := dbClient.DB.Where("LOWER(trigger_type) = ? AND is_active = ?", "cron", true).Find(&cronRules).Error; findErr == nil {
			for _, rule := range cronRules {
				ruleCopy := rule
				// V4-27: a rule of a household with the Automations pack off is not scheduled; the
				// runner checks again when one fires (a switch-off since the last reload).
				if !automationsOn(dbClient, ruleCopy.HouseholdID) {
					continue
				}
				cronExpr, parseErr := automation.ExtractCronSchedule(ruleCopy.ConditionJSON)
				if parseErr != nil {
					slog.Warn("skipping automation rule with invalid cron condition", "rule_id", ruleCopy.ID, "error", parseErr)
					continue
				}
				sched, schedErr := scheduleIn(cronExpr, clockOf(ruleCopy.UserID).Loc)
				if schedErr != nil {
					slog.Error("failed to schedule automation rule cron", "rule_id", ruleCopy.ID, "cron", cronExpr, "error", schedErr)
					continue
				}
				// Held in quiet hours when it reaches its person (a notify action, a workflow's
				// turn, or the turn it runs without a runner) and its condition does not mark it
				// urgent.
				holdable := !automation.CronRuleUrgent(ruleCopy.ConditionJSON) && (runner == nil || automation.NotifiesPerson(ruleCopy))

				jobs = append(jobs, job{name: ruleName(ruleCopy), userID: ruleCopy.UserID, sched: sched, holdable: holdable, run: func() {
					// A scheduled workflow (V4-07): its instruction is a turn for its person.
					if instruction, ok := automation.PromptOf(ruleCopy); ok {
						if !automationsOn(dbClient, ruleCopy.HouseholdID) { // switched off since the last reload
							return
						}
						slog.Info("Running scheduled workflow", "rule_id", ruleCopy.ID)
						startTurn(engine, dbClient, ruleCopy.UserID, instruction, fmt.Sprintf("rule-cron-%d-%d", ruleCopy.ID, time.Now().Unix()))
						return
					}
					slog.Info("Running cron automation rule", "id", ruleCopy.ID, "name", ruleCopy.Name)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()

					ev := events.MutationEvent{
						Topic:       ruleCopy.TriggerTopic,
						Action:      "cron_tick",
						EntityType:  "AutomationRule",
						EntityID:    fmt.Sprint(ruleCopy.ID),
						ActorUserID: ruleCopy.UserID,
						HouseholdID: ruleCopy.HouseholdID,
						FamilyGroup: ruleCopy.FamilyGroup,
						Summary:     fmt.Sprintf("Cron trigger fired for rule %q", ruleCopy.Name),
						Timestamp:   time.Now(),
					}

					if runner != nil {
						execErr := runner.ExecuteAction(ctx, ruleCopy, ev)
						if execErr != nil {
							slog.Error("cron automation rule execution failed", "rule_id", ruleCopy.ID, "error", execErr)
						}
					} else {
						executeWorkflowMessage(engine, dbClient, ruleCopy.UserID, ruleCopy.ActionInstruction, fmt.Sprintf("rule-cron-%d-%d", ruleCopy.ID, time.Now().Unix()))
					}
				}})
			}
		} else {
			slog.Error("failed to load active cron automation rules", "error", findErr)
		}
	}

	live := clockSource(dbClient, tz) // read again at each fire: quiet hours may have changed
	for _, j := range jobs {
		jCopy := j
		workflowCron.Schedule(jCopy.sched, cron.FuncJob(func() { jCopy.fire(live, time.Now()) }))
	}
	currentJobs = jobs

	workflowCron.Start()
	slog.Info("Cron rules reloaded", "cron_rule_count", len(cronRules), "scheduled", len(jobs))
}

// job is one scheduled cron rule (V4-05).
type job struct {
	name     string // for logs and tests: "rule 12"
	userID   string // the person whose clock it follows
	sched    cron.Schedule
	holdable bool // it reaches its person and is not urgent: held during their quiet hours
	run      func()
}

func ruleName(r models.AutomationRule) string { return fmt.Sprintf("rule %d", r.ID) }

// clockFunc returns the clock of the person behind a user ID.
type clockFunc func(userID string) quiet.Clock

// clockSource reads people's clocks from the database, falling back to the install's zone and no
// quiet hours when there is no database or the lookup fails: then a run goes out as it did before
// V4-05, never held on a guess.
func clockSource(dbClient *db.Client, fallback string) clockFunc {
	return func(userID string) quiet.Clock {
		if dbClient == nil {
			return quiet.NewClock(quiet.Settings{}, fallback)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := dbClient.QuietSettings(ctx, userID)
		if err != nil {
			slog.Warn("quiet hours not read; running as if there were none", "user_id", userID, "error", err)
			return quiet.NewClock(quiet.Settings{}, fallback)
		}
		return quiet.NewClock(s, fallback)
	}
}

// memo caches a clockFunc for one pass: one lookup per person.
func memo(f clockFunc) clockFunc {
	seen := map[string]quiet.Clock{}
	return func(userID string) quiet.Clock {
		c, ok := seen[userID]
		if !ok {
			c = f(userID)
			seen[userID] = c
		}
		return c
	}
}

// scheduleIn parses a cron expression (five fields or a descriptor) on a person's clock. An
// expression that names its own zone (CRON_TZ= or TZ=) keeps it.
func scheduleIn(expr string, loc *time.Location) (cron.Schedule, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(expr)
	ownZone := strings.HasPrefix(trimmed, "CRON_TZ=") || strings.HasPrefix(trimmed, "TZ=")
	if spec, ok := sched.(*cron.SpecSchedule); ok && !ownZone && loc != nil {
		spec.Location = loc
	}
	return sched, nil
}

// fire runs the job at now, unless it is holdable and now falls in its person's quiet hours; the
// release check then runs it when they end. It reports whether the job ran.
func (j job) fire(clockOf clockFunc, now time.Time) bool {
	if j.holdable && clockOf(j.userID).QuietAt(now) {
		slog.Info("automation held for quiet hours", "job", j.name)
		return false
	}
	j.run()
	return true
}

// releaseHeld runs, once, each holdable job whose person's quiet hours end in the minute of now
// and that was due at least once while they lasted: however many fires a window held, its person
// gets one run. It returns the names of the jobs it ran.
func releaseHeld(jobs []job, clockOf clockFunc, now time.Time) []string {
	var ran []string
	end := now.Truncate(time.Minute)
	for _, j := range jobs {
		if !j.holdable {
			continue
		}
		start, ok := clockOf(j.userID).WindowEndingAt(now)
		if !ok {
			continue
		}
		// The first fire at or after the window's start, before its end.
		if next := j.sched.Next(start.Add(-time.Second)); next.IsZero() || !next.Before(end) {
			continue
		}
		slog.Info("running an automation held for quiet hours", "job", j.name)
		j.run()
		ran = append(ran, j.name)
	}
	return ran
}

// automationsOn reports whether the household has the Automations pack on (V4-27). A failed
// lookup counts as off: nothing runs on a guess.
func automationsOn(dbClient *db.Client, householdID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	set, err := dbClient.HouseholdPacks(ctx, householdID)
	if err != nil {
		slog.Warn("automation skipped: could not read the household's packs", "household_id", householdID, "error", err)
		return false
	}
	return set.Has(packs.Automations)
}

// startTurn runs an instruction as a turn for a person; tests replace it to see what a scheduled
// workflow starts.
var startTurn = executeWorkflowMessage

// executeWorkflowMessage runs an instruction as a turn for a person, in the conversation they last
// wrote in, so the answer reaches them there.
func executeWorkflowMessage(engine *orchestrator.Engine, dbClient *db.Client, userID, instruction, msgID string) {
	if dbClient == nil || engine == nil {
		return
	}
	log, err := dbClient.GetLastAuditSessionForUser(userID)
	if err != nil || log == nil {
		slog.Warn("failed to get last audit log for workflow", "user_id", userID, "error", err)
		return
	}

	msg := channel.Inbound{
		MessageID:    msgID,
		Conversation: channel.Conversation{ID: log.ChatID},
		Sender:       userID,
		Text:         instruction,
		Channel:      log.Source,
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := engine.ProcessMessage(ctx, msg); err != nil {
			slog.Error("workflow execution failed", "error", err)
		}
	}()
}
