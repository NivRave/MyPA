package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/robfig/cron/v3"
)

// StartCronJobs initializes and starts background scheduling tasks.
func StartCronJobs(engine *orchestrator.Engine) *cron.Cron {
	// Set the timezone to the user's location (e.g. Asia/Jerusalem or UTC)
	// We'll use Local or UTC, but since cron uses the server's local time by default,
	// it's best to specify if we need a specific timezone. We'll just run it at 08:00 system time.
	c := cron.New()

	// Every day at 8:00 AM
	_, err := c.AddFunc("0 8 * * *", func() {
		slog.Info("Cron triggered: Morning Briefing")
		
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		engine.BroadcastProactiveMessage(ctx, "Good morning! Please generate a brief summary of my schedule for today, wish me a good day, and remind me of any important upcoming events.")
	})

	if err != nil {
		slog.Error("failed to add cron job", "error", err)
	}

	c.Start()
	slog.Info("Cron scheduler started")

	return c
}
