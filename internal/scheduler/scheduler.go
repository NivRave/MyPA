package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/robfig/cron/v3"
)

var (
	workflowMu   sync.Mutex
	workflowCron *cron.Cron
)

// StartCronJobs initializes and starts background scheduling tasks.
func StartCronJobs(engine *orchestrator.Engine, dbClient *db.Client, runner *automation.Runner) *cron.Cron {
	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		slog.Warn("Failed to load timezone, using local time", "error", err)
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

		path, err := db.BackupDatabase(databaseURL, backupDir, 3)
		if err != nil {
			slog.Error("Scheduled backup failed", "error", err)
			return
		}
		slog.Info("Scheduled backup completed successfully", "path", path)
	})

	// Every day at 8:00 AM
	_, _ = c.AddFunc("0 8 * * *", func() {
		slog.Info("Cron triggered: Morning Briefing")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		engine.BroadcastProactiveMessage(ctx, "Good morning! Please generate a brief summary of my schedule for today, summarize my pending TODO tasks, wish me a good day, and remind me of any important upcoming events.")
	})

	// Every minute, check for scheduled reminders (both personal and shared)
	_, err = c.AddFunc("* * * * *", func() {
		engine.CheckAndSendReminders()
	})

	// Every 5 minutes, reload dynamic workflows and cron automation rules
	_, _ = c.AddFunc("*/5 * * * *", func() {
		ReloadWorkflows(engine, dbClient, runner)
	})

	if err != nil {
		slog.Error("failed to add cron job", "error", err)
	}

	c.Start()
	slog.Info("Cron scheduler started")

	// Start workflows and cron rules immediately
	ReloadWorkflows(engine, dbClient, runner)

	return c
}

// ReloadWorkflows fetches all active workflows and cron automation rules from the DB and rebuilds the workflow cron.
// Concurrency-safe via workflowMu.
func ReloadWorkflows(engine *orchestrator.Engine, dbClient *db.Client, runner *automation.Runner) {
	workflowMu.Lock()
	defer workflowMu.Unlock()

	if workflowCron != nil {
		workflowCron.Stop()
	}

	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		loc = time.Local
	}

	workflowCron = cron.New(cron.WithLocation(loc))

	// 1. Load legacy workflows
	var workflows []models.Workflow
	if dbClient != nil {
		var err error
		workflows, err = dbClient.GetActiveWorkflows()
		if err != nil {
			slog.Error("failed to get active workflows", "error", err)
		} else {
			for _, wf := range workflows {
				wfCopy := wf // prevent loop variable capture issue
				_, schedErr := workflowCron.AddFunc(wfCopy.CronExpression, func() {
					slog.Info("Running legacy workflow", "id", wfCopy.ID, "name", wfCopy.Name)
					executeWorkflowMessage(engine, dbClient, wfCopy.UserID, wfCopy.Instruction, fmt.Sprintf("wf-%d-%d", wfCopy.ID, time.Now().Unix()))
				})
				if schedErr != nil {
					slog.Error("failed to schedule legacy workflow", "workflow_id", wfCopy.ID, "cron", wfCopy.CronExpression, "error", schedErr)
				}
			}
		}
	}

	// 2. Load active cron automation rules (BUG-05)
	var cronRules []models.AutomationRule
	if dbClient != nil && dbClient.DB != nil {
		if findErr := dbClient.DB.Where("LOWER(trigger_type) = ? AND is_active = ?", "cron", true).Find(&cronRules).Error; findErr == nil {
			for _, rule := range cronRules {
				ruleCopy := rule
				cronExpr, parseErr := automation.ExtractCronSchedule(ruleCopy.ConditionJSON)
				if parseErr != nil {
					slog.Warn("skipping automation rule with invalid cron condition", "rule_id", ruleCopy.ID, "error", parseErr)
					continue
				}

				_, schedErr := workflowCron.AddFunc(cronExpr, func() {
					slog.Info("Running cron automation rule", "id", ruleCopy.ID, "name", ruleCopy.Name)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()

					ev := events.MutationEvent{
						Topic:       ruleCopy.TriggerTopic,
						Action:      "cron_tick",
						EntityType:  "AutomationRule",
						EntityID:    fmt.Sprint(ruleCopy.ID),
						ActorUserID: ruleCopy.UserID,
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
				})

				if schedErr != nil {
					slog.Error("failed to schedule automation rule cron", "rule_id", ruleCopy.ID, "cron", cronExpr, "error", schedErr)
				}
			}
		} else {
			slog.Error("failed to load active cron automation rules", "error", findErr)
		}
	}

	workflowCron.Start()
	slog.Info("Workflows and cron rules reloaded", "legacy_count", len(workflows), "cron_rule_count", len(cronRules))
}

func executeWorkflowMessage(engine *orchestrator.Engine, dbClient *db.Client, userID, instruction, msgID string) {
	if dbClient == nil || engine == nil {
		return
	}
	log, err := dbClient.GetLastAuditSessionForUser(userID)
	if err != nil || log == nil {
		slog.Warn("failed to get last audit log for workflow", "user_id", userID, "error", err)
		return
	}

	msg := models.Message{
		ID:     msgID,
		ChatID: log.ChatID,
		UserID: userID,
		Text:   instruction,
		Source: log.Source,
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := engine.ProcessMessage(ctx, msg); err != nil {
			slog.Error("workflow execution failed", "error", err)
		}
	}()
}
