package main

import (
	"context"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
)

const (
	// doctorBackupDir is the backup volume mounted in docker-compose.yml (the directory the
	// nightly backup and /backup write to).
	doctorBackupDir = "/backups"
	// doctorBackupMaxAge: backups run nightly at 02:00, so a newest backup older than a day plus
	// two hours of slack means last night's run did not produce one.
	doctorBackupMaxAge = 26 * time.Hour
	// doctorQueueBacklog is the number of waiting messages from which a queue counts as backed
	// up. A healthy household drains its queues within seconds.
	doctorQueueBacklog = 20
)

// newHealthReporter binds the /doctor checks (V4-17, internal/doctor) to this install: Telegram's
// own view of the webhook, the Google tokens in Redis, the Twilio settings, RabbitMQ queue depths,
// the backup directory and the schema version in Postgres.
func newHealthReporter(cfg *config.Config, tg *telegram.Client, store *state.Store, dbClient *db.Client, oauthCfg *calendar.OAuthConfig, backupDir string) *doctor.Doctor {
	return doctor.New(
		doctor.TelegramWebhook{
			Configured: cfg.Telegram.BotToken != "",
			WantURL:    cfg.Telegram.WebhookURL,
			Info: func(ctx context.Context) (doctor.WebhookInfo, error) {
				info, err := tg.GetWebhookInfo(ctx)
				if err != nil {
					return doctor.WebhookInfo{}, err
				}
				return doctor.WebhookInfo{
					URL:          info.URL,
					Pending:      info.PendingUpdateCount,
					LastErrorAt:  info.LastErrorAt(),
					LastErrorMsg: info.LastErrorMessage,
				}, nil
			},
		},
		doctor.GoogleAccounts{
			Configured:    cfg.Google.ClientID != "",
			FamilyMembers: dbClient.GetUsersByFamilyGroup,
			Token:         store.GetOAuthToken,
			Refresh:       doctor.RefreshWith(oauthCfg.TokenSource),
		},
		doctor.WhatsApp{
			Configured:        cfg.Twilio.AccountSID != "",
			Sender:            cfg.Twilio.FromNumber,
			StatusCallbackURL: cfg.Twilio.StatusCallbackURL,
		},
		doctor.Queues{
			Names:       []string{broker.InboundMessagesQueue, broker.AuditEventsQueue},
			BacklogWarn: doctorQueueBacklog,
			Depth: func(ctx context.Context, queue string) (int, error) {
				return broker.QueueDepth(ctx, cfg.RabbitMQ.URL, queue)
			},
		},
		doctor.Backups{
			MaxAge: doctorBackupMaxAge,
			Latest: func() (string, time.Time, error) { return db.LatestBackupInfo(backupDir) },
		},
		doctor.Schema{
			Version: func(ctx context.Context) (int64, int64, error) {
				sqlDB, err := dbClient.DB.DB()
				if err != nil {
					return 0, 0, err
				}
				return db.SchemaVersion(ctx, sqlDB)
			},
		},
	)
}
