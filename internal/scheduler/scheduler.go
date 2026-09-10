package scheduler

import (
	"context"
	"log/slog"
	"os"
	"time"
    "fmt"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/models"
	"github.com/robfig/cron/v3"
)

var workflowCron *cron.Cron

// StartCronJobs initializes and starts background scheduling tasks.
func StartCronJobs(engine *orchestrator.Engine, dbClient *db.Client) *cron.Cron {
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

	// Every minute, check for scheduled reminders
	_, err = c.AddFunc("* * * * *", func() {
		engine.CheckAndSendReminders()
	})

	// Every 5 minutes, reload the dynamic workflows
	_, _ = c.AddFunc("*/5 * * * *", func() {
		ReloadWorkflows(engine, dbClient)
	})

	if err != nil {
		slog.Error("failed to add cron job", "error", err)
	}

	c.Start()
	slog.Info("Cron scheduler started")

	// Start workflows immediately
	ReloadWorkflows(engine, dbClient)

	return c
}

// ReloadWorkflows fetches all active workflows from the DB and rebuilds the workflow cron.
func ReloadWorkflows(engine *orchestrator.Engine, dbClient *db.Client) {
	if workflowCron != nil {
		workflowCron.Stop()
	}

	loc, err := time.LoadLocation("Asia/Jerusalem")
	if err != nil {
		loc = time.Local
	}

	workflowCron = cron.New(cron.WithLocation(loc))

	workflows, err := dbClient.GetActiveWorkflows()
	if err != nil {
		slog.Error("failed to get active workflows", "error", err)
		return
	}

	for _, wf := range workflows {
		wfCopy := wf // prevent loop variable capture issue
		_, err := workflowCron.AddFunc(wfCopy.CronExpression, func() {
			slog.Info("Running workflow", "id", wfCopy.ID, "name", wfCopy.Name)
			
			// Determine the user's chat ID and source
			log, err := dbClient.GetLastAuditSessionForUser(wfCopy.UserID)
			if err != nil || log == nil {
				slog.Warn("failed to get last audit log for workflow", "user_id", wfCopy.UserID, "error", err)
				return
			}
			
			msg := models.Message{
				ID:     fmt.Sprintf("wf-%d-%d", wfCopy.ID, time.Now().Unix()),
				ChatID: log.ChatID,
				UserID: wfCopy.UserID,
				Text:   wfCopy.Instruction,
				Source: log.Source,
			}

			// We need a context
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if err := engine.ProcessMessage(ctx, msg); err != nil {
					slog.Error("workflow execution failed", "error", err)
				}
			}()
		})
		
		if err != nil {
			slog.Error("failed to schedule workflow", "workflow_id", wfCopy.ID, "cron", wfCopy.CronExpression, "error", err)
		}
	}

	workflowCron.Start()
	slog.Info("Workflows reloaded", "count", len(workflows))
}
