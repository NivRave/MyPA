package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validTGSecret = "valid_telegram-secret123"

func workerOK() Config {
	return Config{
		Server: ServerConfig{
			DashboardAPISecret:   "dash-secret-abc",
			DashboardFamilyGroup: "TheFamily",
			JWTSecret:            "jwt-secret-def",
		},
		Encryption: EncryptionConfig{Key: "enc-key-ghi"},
		Database:   DatabaseConfig{URL: "postgres://u:p@db:5432/x"},
		Redis:      RedisConfig{URL: "redis://redis:6379/0"},
		RabbitMQ:   RabbitMQConfig{URL: "amqp://u:p@rmq:5672/"},
		Gemini:     GeminiConfig{APIKey: "gemini-key"},
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		service string
		mutate  func(c *Config)
		base    func() Config
		wantErr []string // substrings; empty = expect nil
	}{
		// --- unknown service ---
		{name: "UnknownService", service: "nope", base: func() Config { return Config{} }, wantErr: []string{"unknown service"}},

		// --- ingress (the old gateway) ---
		{name: "Ingress_MinimalOK", service: RoleIngress,
			base: func() Config { return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}} }},
		{name: "Ingress_MissingRabbit", service: RoleIngress,
			base: func() Config { return Config{} }, wantErr: []string{"RABBITMQ_URL"}},
		{name: "Ingress_TelegramWithoutSecret", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{Telegram: telegram.Config{BotToken: "1:a"}}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Ingress_TelegramWithSecretOK", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{Telegram: telegram.Config{BotToken: "1:a", SecretToken: validTGSecret}}}
			}},
		{name: "Ingress_TelegramSecretBadChars", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{Telegram: telegram.Config{BotToken: "1:a", SecretToken: "has spaces!"}}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Ingress_TelegramSecretTooLong", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{Telegram: telegram.Config{BotToken: "1:a", SecretToken: strings.Repeat("a", 257)}}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Ingress_WebhookURLWithoutBotToken", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{Telegram: telegram.Config{WebhookURL: "https://x/webhook/telegram", SecretToken: validTGSecret}}}
			}, wantErr: []string{"TELEGRAM_BOT_TOKEN"}},
		{name: "Ingress_TwilioSIDWithoutToken", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{WhatsApp: twilio.Config{AccountSID: "AC1"}}}
			}, wantErr: []string{"TWILIO_AUTH_TOKEN"}},
		{name: "Ingress_TwilioOK", service: RoleIngress,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Channels: adapters.Config{WhatsApp: twilio.Config{AccountSID: "AC1", AuthToken: "t"}}}
			}},

		// --- worker (the old orchestrator) ---
		{name: "Worker_AllSetOK", service: RoleWorker, base: workerOK},
		{name: "Worker_NothingSet_ListsEveryKey", service: RoleWorker,
			base:    func() Config { return Config{} },
			wantErr: []string{"ENCRYPTION_KEY", "JWT_SECRET", "DASHBOARD_API_SECRET", "DASHBOARD_FAMILY_GROUP", "DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "GEMINI_API_KEY"}},
		{name: "Worker_WhitespaceKeyIsMissing", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) { c.Encryption.Key = "   " }, wantErr: []string{"ENCRYPTION_KEY"}},
		{name: "Worker_JWTEqualsEncryptionKey", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) { c.Server.JWTSecret = c.Encryption.Key }, wantErr: []string{"JWT_SECRET must differ"}},
		{name: "Worker_PlaceholderEncryptionKey", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) { c.Encryption.Key = "your-32-byte-encryption-key" }, wantErr: []string{"ENCRYPTION_KEY", "placeholder"}},
		{name: "Worker_PlaceholderDatabaseURL", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) {
				c.Database.URL = "postgres://<YOUR_DB_USER>:<YOUR_DB_PASSWORD>@postgres:5432/<YOUR_DB_NAME>"
			}, wantErr: []string{"DATABASE_URL", "placeholder"}},
		{name: "Worker_TwilioSIDWithoutToken", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) { c.Channels.WhatsApp.AccountSID = "AC1" }, wantErr: []string{"TWILIO_AUTH_TOKEN"}},
		// BL-8: optional, but if set it must be an absolute https URL Twilio can call back.
		{name: "Worker_StatusCallbackHTTPSOK", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) { c.Channels.WhatsApp.StatusCallbackURL = "https://gw.example/webhook/twilio/status" }},
		{name: "Worker_StatusCallbackNotHTTPS", service: RoleWorker, base: workerOK,
			mutate:  func(c *Config) { c.Channels.WhatsApp.StatusCallbackURL = "http://gw.example/webhook/twilio/status" },
			wantErr: []string{"TWILIO_STATUS_CALLBACK_URL"}},
		{name: "Worker_StatusCallbackRelative", service: RoleWorker, base: workerOK,
			mutate:  func(c *Config) { c.Channels.WhatsApp.StatusCallbackURL = "/webhook/twilio/status" },
			wantErr: []string{"TWILIO_STATUS_CALLBACK_URL"}},
		{name: "Worker_StatusCallbackPlaceholder", service: RoleWorker, base: workerOK,
			mutate: func(c *Config) {
				c.Channels.WhatsApp.StatusCallbackURL = "https://your-ngrok-url.ngrok-free.dev/webhook/twilio/status"
			},
			wantErr: []string{"TWILIO_STATUS_CALLBACK_URL"}},

		// --- all (V4-01: ingress and worker in one process, the default role) ---
		// It needs everything the worker needs, and the ingress checks run too: one process now
		// receives the webhooks, so a Telegram bot without its secret token is refused here.
		{name: "All_AllSetOK", service: RoleAll, base: workerOK},
		{name: "All_NothingSet_ListsEveryKey", service: RoleAll,
			base:    func() Config { return Config{} },
			wantErr: []string{"ENCRYPTION_KEY", "JWT_SECRET", "DASHBOARD_API_SECRET", "DASHBOARD_FAMILY_GROUP", "DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "GEMINI_API_KEY"}},
		{name: "All_TelegramWithoutSecret", service: RoleAll, base: workerOK,
			mutate: func(c *Config) { c.Channels.Telegram.BotToken = "1:a" }, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "All_TelegramWithSecretOK", service: RoleAll, base: workerOK,
			mutate: func(c *Config) { c.Channels.Telegram.BotToken = "1:a"; c.Channels.Telegram.SecretToken = validTGSecret }},
		{name: "All_TelegramSecretBadChars", service: RoleAll, base: workerOK,
			mutate:  func(c *Config) { c.Channels.Telegram.BotToken = "1:a"; c.Channels.Telegram.SecretToken = "has spaces!" },
			wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "All_WebhookURLWithoutBotToken", service: RoleAll, base: workerOK,
			mutate: func(c *Config) {
				c.Channels.Telegram.WebhookURL = "https://x/webhook/telegram"
				c.Channels.Telegram.SecretToken = validTGSecret
			}, wantErr: []string{"TELEGRAM_BOT_TOKEN"}},
		{name: "All_JWTEqualsEncryptionKey", service: RoleAll, base: workerOK,
			mutate: func(c *Config) { c.Server.JWTSecret = c.Encryption.Key }, wantErr: []string{"JWT_SECRET must differ"}},
		{name: "All_TwilioSIDWithoutToken", service: RoleAll, base: workerOK,
			mutate: func(c *Config) { c.Channels.WhatsApp.AccountSID = "AC1" }, wantErr: []string{"TWILIO_AUTH_TOKEN"}},
		{name: "All_StatusCallbackNotHTTPS", service: RoleAll, base: workerOK,
			mutate:  func(c *Config) { c.Channels.WhatsApp.StatusCallbackURL = "http://gw.example/webhook/twilio/status" },
			wantErr: []string{"TWILIO_STATUS_CALLBACK_URL"}},

		// --- the services V4-01 removed are no longer accepted ---
		{name: "Removed_Proxy", service: "proxy", base: workerOK, wantErr: []string{"unknown service"}},
		{name: "Removed_Gateway", service: "gateway", base: workerOK, wantErr: []string{"unknown service"}},
		{name: "Removed_Orchestrator", service: "orchestrator", base: workerOK, wantErr: []string{"unknown service"}},

		// --- audit-worker ---
		{name: "AuditWorker_OK", service: ServiceAuditWorker,
			base: func() Config {
				return Config{Database: DatabaseConfig{URL: "postgres://x"}, RabbitMQ: RabbitMQConfig{URL: "amqp://x"}}
			}},
		{name: "AuditWorker_Missing", service: ServiceAuditWorker, base: func() Config { return Config{} },
			wantErr: []string{"DATABASE_URL", "RABBITMQ_URL"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.base()
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			err := c.Validate(tc.service)
			if len(tc.wantErr) == 0 {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tc.wantErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", name))
	require.NoError(t, err)
	return string(data)
}

// V4-01: the app is one binary with roles, and the proxy that needed the two upstream addresses is
// gone. No role may demand them, and the order of Roles is the one `--role` documents.
func TestRoles_AreTheAppRolesAndNeedNoUpstreamAddresses(t *testing.T) {
	assert.Equal(t, []string{RoleAll, RoleIngress, RoleWorker}, Roles())
	for _, key := range RequiredEnvKeys() {
		assert.NotContains(t, []string{"GATEWAY_URL", "ORCHESTRATOR_URL"}, key)
	}
	for _, role := range Roles() {
		assert.NotContains(t, requiredKeys[role], "GATEWAY_URL", role)
		assert.NotContains(t, requiredKeys[role], "ORCHESTRATOR_URL", role)
	}
}

// Every key that Validate can demand must be documented in .env.example.
func TestEnvExampleListsEveryRequiredKey(t *testing.T) {
	content := readRepoFile(t, ".env.example")
	for _, key := range RequiredEnvKeys() {
		assert.Contains(t, content, key+"=", ".env.example must list %s", key)
	}
}
