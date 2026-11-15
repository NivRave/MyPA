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
	if err := cfg.Validate(config.ServiceAuditWorker); err != nil {
		slog.Error("invalid audit-worker configuration — refusing to start", "error", err)
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
			_ = archiveOldSessions(dbClient)
		}
	}
}

func getArchiveDir() string {
	if dir := os.Getenv("AUDIT_ARCHIVE_DIR"); dir != "" {
		return dir
	}
	if fi, err := os.Stat("/app/archives"); err == nil && fi.IsDir() {
		return "/app/archives"
	}
	return "."
}

func archiveOldSessions(dbClient *db.Client, optionalDir ...string) error {
	slog.Info("running daily archive routine for old audit sessions")
	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour)

	var sessions []models.AuditSession
	result := dbClient.DB.Where("start_time < ?", thirtyDaysAgo).Find(&sessions)
	if result.Error != nil {
		slog.Error("failed to query old sessions", "error", result.Error)
		return result.Error
	}

	if len(sessions) == 0 {
		return nil
	}

	archiveDir := getArchiveDir()
	if len(optionalDir) > 0 && optionalDir[0] != "" {
		archiveDir = optionalDir[0]
	}

	// P2-9: gzip JSONL written atomically through an ArchiveSink (local disk today).
	var sink ArchiveSink = localGzipSink{dir: archiveDir}
	filePath, err := sink.Write(fmt.Sprintf("audit_archive_%d", time.Now().Unix()), sessions)
	if err != nil {
		slog.Error("failed to write audit archive; sessions kept in the database", "dir", archiveDir, "error", err)
		return err
	}

	// Delete only successfully archived sessions from DB
	sessionIDs := make([]string, len(sessions))
	for i, s := range sessions {
		sessionIDs[i] = s.ID
	}
	delResult := dbClient.DB.Where("id IN ?", sessionIDs).Delete(&models.AuditSession{})
	if delResult.Error != nil {
		slog.Error("failed to delete archived sessions", "error", delResult.Error)
		return delResult.Error
	}

	slog.Info("archived and deleted old sessions", "count", len(sessions), "file", filePath)
	return nil
}

