package scheduler

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/robfig/cron/v3"
)

// StartCronJobs initializes and starts background scheduling tasks.
func StartCronJobs(engine *orchestrator.Engine) *cron.Cron {
	// Set the timezone to the user's location (e.g. Asia/Jerusalem or UTC)
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

	if err != nil {
		slog.Error("failed to add cron job", "error", err)
	}

	c.Start()
	slog.Info("Cron scheduler started")

	return c
}
