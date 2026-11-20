package main

import (
	"context"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"time"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/state"
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

// healthChecker is implemented by a channel adapter that has its own /doctor check. activity
// tells it when each of a household's accounts on it last wrote (V4-41).
type healthChecker interface {
	HealthCheck(activity channel.ActivitySource) doctor.Check
}

// channelChecks returns the /doctor check of every registered adapter that has one, in
// registration order.
func channelChecks(channels *channel.Registry, activity channel.ActivitySource) []doctor.Check {
	var checks []doctor.Check
	for _, a := range channels.All() {
		if hc, ok := a.(healthChecker); ok {
			checks = append(checks, hc.HealthCheck(activity))
		}
	}
	return checks
}

// accountActivity is the channel.ActivitySource of the database: the household's members (V4-30)
// and, for each, their accounts on the channel with when each last wrote (V4-32). A users row and
// the person it became share an ID until V4-44.
func accountActivity(dbClient *db.Client) channel.ActivitySource {
	return func(ctx context.Context, householdID, channelName string) ([]channel.AccountActivity, error) {
		scope, err := identity.ForStoredRow(ctx, dbClient, householdID, "")
		if err != nil {
			return nil, err
		}
		members, err := dbClient.UsersInHousehold(ctx, scope)
		if err != nil {
			return nil, err
		}
		var out []channel.AccountActivity
		for _, m := range members {
			accounts, err := dbClient.AccountsOfPerson(ctx, m.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range accounts {
				if a.Channel != channelName {
					continue
				}
				act := channel.AccountActivity{Person: m.Name}
				if act.Person == "" {
					act.Person = doctor.MaskTail(m.ID)
				}
				if a.LastInboundAt != nil {
					act.LastInboundAt = *a.LastInboundAt
				}
				out = append(out, act)
			}
		}
		return out, nil
	}
}

// newHealthReporter binds the /doctor checks (V4-17, internal/doctor) to this install: each
// channel adapter's own check (V4-31), the Google tokens in Redis, RabbitMQ queue depths, the
// backup directory and the schema version in Postgres.
func newHealthReporter(cfg *config.Config, channels *channel.Registry, store *state.Store, dbClient *db.Client, oauthCfg *calendar.OAuthConfig, backupDir string) *doctor.Doctor {
	checks := channelChecks(channels, accountActivity(dbClient))
	checks = append(checks,
		doctor.GoogleAccounts{
			Configured: cfg.Google.ClientID != "",
			HouseholdMembers: func(householdID string) ([]models.User, error) {
				// The household comes from the admin's own row (V4-30); the scope is built from it.
				scope, err := identity.ForStoredRow(context.Background(), dbClient, householdID, "")
				if err != nil {
					return nil, err
				}
				return dbClient.UsersInHousehold(context.Background(), scope)
			},
			Token:   store.GetOAuthToken,
			Refresh: doctor.RefreshWith(oauthCfg.TokenSource),
		},
		doctor.AllowedUsers{
			Set:        len(cfg.ParseAllowedUsers()) > 0,
			Households: dbClient.CountHouseholds,
		},
		doctor.Owners{Ownerless: dbClient.HouseholdsWithoutOwner}, // V4-35
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
	return doctor.New(checks...)
}
