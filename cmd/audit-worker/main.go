package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var dbClient *db.Client
	for i := 0; i < 5; i++ {
		dbClient, err = db.NewClient(cfg.Database.URL)
		if err == nil {
			break
		}
		slog.Warn("failed to connect to db, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		slog.Error("failed to connect to db after retries", "error", err)
		os.Exit(1)
	}

	consumer, err := broker.NewConsumer(cfg.RabbitMQ.URL, "audit.events")
	if err != nil {
		slog.Error("failed to initialize rabbitmq consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consumer
	go func() {
		slog.Info("audit-worker started consuming audit.events")
		err := consumer.ConsumeRaw(func(data []byte) error {
			var session models.AuditSession
			if err := json.Unmarshal(data, &session); err != nil {
				slog.Error("failed to unmarshal audit session", "error", err)
				return nil // Don't retry poison messages
			}

			if err := dbClient.InsertAuditSession(session); err != nil {
				return err // Retry on DB failure
			}
			slog.Info("inserted audit session", "session_id", session.ID)
			return nil
		})
		if err != nil {
			slog.Error("consumer stopped", "error", err)
		}
	}()

	// Start Archiver
	go startArchiver(ctx, dbClient)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("audit-worker shutting down")
}

func startArchiver(ctx context.Context, dbClient *db.Client) {
	slog.Info("starting daily archiver cron routine")
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			archiveOldSessions(dbClient)
		}
	}
}

func archiveOldSessions(dbClient *db.Client) {
	slog.Info("running daily archive routine for old audit sessions")
	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour)

	var sessions []models.AuditSession
	result := dbClient.DB.Where("start_time < ?", thirtyDaysAgo).Find(&sessions)
	if result.Error != nil {
		slog.Error("failed to query old sessions", "error", result.Error)
		return
	}

	if len(sessions) == 0 {
		return
	}

	// Dump to file
	filename := fmt.Sprintf("audit_archive_%d.jsonl", time.Now().Unix())
	f, err := os.Create(filename)
	if err != nil {
		slog.Error("failed to create archive file", "error", err)
		return
	}
	
	encoder := json.NewEncoder(f)
	for _, s := range sessions {
		_ = encoder.Encode(s)
	}
	f.Close()

	// Delete from DB
	delResult := dbClient.DB.Where("start_time < ?", thirtyDaysAgo).Delete(&models.AuditSession{})
	if delResult.Error != nil {
		slog.Error("failed to delete archived sessions", "error", delResult.Error)
		return
	}

	slog.Info("archived and deleted old sessions", "count", len(sessions), "file", filename)
}
