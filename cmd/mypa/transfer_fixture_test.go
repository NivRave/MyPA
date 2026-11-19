package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/pgvector/pgvector-go"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"golang.org/x/oauth2"
)

// seedEveryOtherTable adds one row to every table that install.seed leaves empty, so that the
// seeded database holds data of every feature: baby, house and vehicles, finance, automations
// and the personal CRM, sharing, calendar, memory, undo history, audit and pairing. Together with
// seed, every table of the v3.4.0 schema has at least one row.
func seedEveryOtherTable(i *install) {
	i.t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	later := now.Add(72 * time.Hour)
	embedding := make([]float32, 3072)
	embedding[3] = 1

	baby := models.BabyProfile{FamilyGroup: "Levi", Name: "Noa", DateOfBirth: now.AddDate(0, -4, 0), UpdatedByUserID: "1001"}
	require.NoError(i.t, i.client.DB.Create(&baby).Error)
	sessionID := uuid.NewString()

	rows := []any{
		// Core
		&models.PairingCode{Code: "482913", Role: "family", FamilyGroup: "Levi", ExpiresAt: later},
		&models.Memory{UserID: "1001", Scope: "personal", Fact: "Prefers tea to coffee.", Embedding: pgvector.NewVector(embedding)},
		&models.Workflow{UserID: "1001", Name: "Morning summary", CronExpression: "0 7 * * *", Instruction: "Summarise my day", IsActive: true},
		&models.AuditSession{ID: sessionID, UserID: "1001", ChatID: "1001", Source: "telegram", UserPrompt: "hello", FinalResponse: "hi", StartTime: now, EndTime: now, Status: "success"},
		&models.AuditEvent{ID: uuid.NewString(), SessionID: sessionID, EventType: "tool_execution", ActionName: "get_current_time", RequestPayload: "{}", ResponsePayload: "{}", StartTime: now, EndTime: now},
		// Durability
		&models.MutationLog{ID: uuid.NewString(), UserID: "1001", FamilyGroup: "Levi", Topic: "house", Action: "create", EntityType: "house_contract_record", EntityID: "1", BeforeJSON: "{}", AfterJSON: "{}", Summary: "Added the rental contract"},
		&models.IdempotencyRecord{Key: "idem-1", UserID: "1001", ToolName: "log_transaction", ResponseSummary: "done", ExpiresAt: later},
		// Memory
		&models.EntityProfile{UserID: "1001", FamilyGroup: "Levi", Topic: "house", EntityType: "appliance", EntityName: "Boiler", AttributesJSON: "{}", UpdatedByUserID: "1001"},
		// Sharing
		&models.SharedSpace{FamilyGroup: "Levi", Topic: "house", OwnerUserID: "1001", SharedWithUserID: "1002", CanView: true, CanEdit: true, NotifyMode: "instant"},
		&models.SharedTaskReminder{FamilyGroup: "Levi", Topic: "house", Title: "Renew the car insurance", DueTime: &later, CreatedByUserID: "1001"},
		// Calendar
		&models.ScheduleEvent{UserID: "1001", FamilyGroup: "Levi", Topic: "house", TagsJSON: "[]", Title: "Plumber", StartTime: later, EndTime: later.Add(time.Hour), CreatedByUserID: "1001"},
		&models.TopicCalendarConfig{UserID: "1001", Topic: "house", ColorID: "5", PrefixTag: "[House]"},
		// Baby
		&models.BabyCareLog{BabyID: baby.ID, FamilyGroup: "Levi", LogType: "feeding", ValueNumeric: 120, Unit: "ml", Timestamp: now, LoggedByUserID: "1002"},
		&models.BabyInventoryItem{FamilyGroup: "Levi", ItemName: "Diapers", QuantityRemaining: 40, Unit: "pcs", LowThreshold: 10},
		// House and vehicles
		&models.HouseContractRecord{FamilyGroup: "Levi", RecordType: "rental", Title: "Apartment lease", Amount: 5200, Currency: "ILS", NoticePeriodDays: 60, KeyTermsJSON: "{}", CreatedByUserID: "1001"},
		&models.HomeMaintenanceItem{FamilyGroup: "Levi", TaskName: "Service the boiler", Category: "heating", IntervalDays: 365, NextDueAt: later, UpdatedByUserID: "1001"},
		&models.VehicleAsset{FamilyGroup: "Levi", Name: "Family car", LicensePlate: "00-000-00", CurrentMileageKm: 42000},
		// Finance
		&models.FinancialTransaction{UserID: "1001", FamilyGroup: "Levi", Type: "expense", Category: "groceries", Merchant: "Market", Amount: 180.5, Currency: "ILS", TransactionDate: now, IsShared: true, Source: "manual"},
		&models.CategoryBudget{FamilyGroup: "Levi", Category: "groceries", MonthlyLimit: 3000, Currency: "ILS", AlertThresholdPct: 80},
		&models.RecurringBill{FamilyGroup: "Levi", Name: "Electricity", Category: "utilities", ExpectedAmount: 400, Currency: "ILS", BillingCycle: "monthly", NextDueDay: 10},
		// Automations, preferences and the personal CRM
		&models.ExternalAPIConnector{UserID: "1001", FamilyGroup: "Levi", Name: "weather", BaseURL: "https://api.example.com", DefaultMethod: "GET", EncryptedAuthHeaders: []byte("ciphertext"), Description: "Weather"},
		&models.AutomationRule{UserID: "1001", FamilyGroup: "Levi", Name: "Budget alert", TriggerType: "event", TriggerTopic: "finance", ConditionJSON: "{}", ActionInstruction: "Tell me", IsActive: true},
		&models.UserPreference{UserID: "1001", PreferredLanguage: "he", TonePersona: "warm", DefaultCurrency: "ILS", QuietHoursStart: "22:00", QuietHoursEnd: "07:00", BriefingTopicsJSON: "[]", StandingInstructions: "Keep answers short."},
		&models.PersonalCRMContact{UserID: "1001", FamilyGroup: "Levi", PersonName: "Grandma Rina", Relationship: "grandmother", ImportantDatesJSON: "{}"},
	}
	for _, row := range rows {
		require.NoError(i.t, i.client.DB.Create(row).Error, "seed %T", row)
	}
}

// tableCounts returns the number of rows in every table of the public schema, except the
// migration bookkeeping table.
func tableCounts(t *testing.T, sqlDB *sql.DB) map[string]int {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name <> 'goose_db_version' ORDER BY table_name`)
	require.NoError(t, err)
	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Close())
	counts := map[string]int{}
	for _, name := range names {
		var n int
		require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM "`+name+`"`).Scan(&n))
		counts[name] = n
	}
	return counts
}

func readCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err, "the baseline row counts are missing")
	counts := map[string]int{}
	require.NoError(t, json.Unmarshal(body, &counts))
	return counts
}

func writeCounts(t *testing.T, path string, counts map[string]int) {
	t.Helper()
	body, err := json.MarshalIndent(counts, "", "  ") // map keys are written sorted
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o644))
}

// A real RabbitMQ of the production image, shared by the tests of this package that need one.
var (
	rabbitOnce sync.Once
	rabbitURL  string
	rabbitErr  error
)

func rabbitMQ(t *testing.T) string {
	t.Helper()
	rabbitOnce.Do(func() {
		ctx := context.Background()
		container, err := rabbitmq.Run(ctx, "rabbitmq:4.3.7-management-alpine@sha256:eb338b3fc8f5c251028c0022ac0de3fe1db2d599d1c6a08b45898f756f248881")
		if err != nil {
			rabbitErr = err
			return
		}
		rabbitURL, rabbitErr = container.AmqpURL(ctx)
	})
	require.NoError(t, rabbitErr, "RabbitMQ test container")
	return rabbitURL
}

// declareAppQueues creates the queues a started app would have created.
func declareAppQueues(t *testing.T, amqpURL string) {
	t.Helper()
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()
	for _, queue := range transferQueues {
		_, err := broker.DeclareDLQTopology(ch, queue)
		require.NoError(t, err)
	}
}

// doctorReport runs the production /doctor wiring (newHealthReporter) against an install, as the
// admin with the given platform ID. Telegram and Google are answered by local fakes that report a
// correctly registered webhook and accept every token renewal; RabbitMQ, Redis, Postgres and the
// backup directory are the install's real ones.
func doctorReport(t *testing.T, i *install, amqpURL, adminID string) doctor.Report {
	t.Helper()
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(google.Close)
	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://mypa.example/oauth/callback"})
	oauthCfg.SetEndpoint(oauth2.Endpoint{TokenURL: google.URL, AuthStyle: oauth2.AuthStyleInParams})

	cfg := &config.Config{}
	cfg.Telegram.BotToken = "bot-token"
	cfg.Telegram.WebhookURL = "https://mypa.example/webhook/telegram"
	cfg.Google.ClientID = "id"
	cfg.Twilio.AccountSID = "ACtest"
	cfg.Twilio.FromNumber = "whatsapp:+12025550199"
	cfg.Twilio.StatusCallbackURL = "https://mypa.example/webhook/twilio/status"
	cfg.RabbitMQ.URL = amqpURL
	tg := telegram.NewClient(cfg.Telegram.BotToken, telegram.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://mypa.example/webhook/telegram","pending_update_count":0}`},
	}))

	// The Google check reads sign-ins the way the app does: cache first, then the database copy.
	_ = i.googleSignIn(adminID)

	admin, err := i.client.GetUser(adminID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return newHealthReporter(cfg, tg, i.store, i.client, oauthCfg, i.deps.BackupDir).Run(ctx, doctor.Scope{Admin: admin})
}
