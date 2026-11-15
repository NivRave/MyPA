package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validTGSecret = "valid_telegram-secret123"

func orchestratorOK() Config {
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

		// --- gateway ---
		{name: "Gateway_MinimalOK", service: ServiceGateway,
			base: func() Config { return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}} }},
		{name: "Gateway_MissingRabbit", service: ServiceGateway,
			base: func() Config { return Config{} }, wantErr: []string{"RABBITMQ_URL"}},
		{name: "Gateway_TelegramWithoutSecret", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Telegram: TelegramConfig{BotToken: "1:a"}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Gateway_TelegramWithSecretOK", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Telegram: TelegramConfig{BotToken: "1:a", SecretToken: validTGSecret}}
			}},
		{name: "Gateway_TelegramSecretBadChars", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Telegram: TelegramConfig{BotToken: "1:a", SecretToken: "has spaces!"}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Gateway_TelegramSecretTooLong", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Telegram: TelegramConfig{BotToken: "1:a", SecretToken: strings.Repeat("a", 257)}}
			}, wantErr: []string{"TELEGRAM_SECRET_TOKEN"}},
		{name: "Gateway_WebhookURLWithoutBotToken", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Telegram: TelegramConfig{WebhookURL: "https://x/webhook/telegram", SecretToken: validTGSecret}}
			}, wantErr: []string{"TELEGRAM_BOT_TOKEN"}},
		{name: "Gateway_TwilioSIDWithoutToken", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Twilio: TwilioConfig{AccountSID: "AC1"}}
			}, wantErr: []string{"TWILIO_AUTH_TOKEN"}},
		{name: "Gateway_TwilioOK", service: ServiceGateway,
			base: func() Config {
				return Config{RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Twilio: TwilioConfig{AccountSID: "AC1", AuthToken: "t"}}
			}},

		// --- orchestrator ---
		{name: "Orchestrator_AllSetOK", service: ServiceOrchestrator, base: orchestratorOK},
		{name: "Orchestrator_NothingSet_ListsEveryKey", service: ServiceOrchestrator,
			base:    func() Config { return Config{} },
			wantErr: []string{"ENCRYPTION_KEY", "JWT_SECRET", "DASHBOARD_API_SECRET", "DASHBOARD_FAMILY_GROUP", "DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "GEMINI_API_KEY"}},
		{name: "Orchestrator_WhitespaceKeyIsMissing", service: ServiceOrchestrator, base: orchestratorOK,
			mutate: func(c *Config) { c.Encryption.Key = "   " }, wantErr: []string{"ENCRYPTION_KEY"}},
		{name: "Orchestrator_JWTEqualsEncryptionKey", service: ServiceOrchestrator, base: orchestratorOK,
			mutate: func(c *Config) { c.Server.JWTSecret = c.Encryption.Key }, wantErr: []string{"JWT_SECRET must differ"}},
		{name: "Orchestrator_PlaceholderEncryptionKey", service: ServiceOrchestrator, base: orchestratorOK,
			mutate: func(c *Config) { c.Encryption.Key = "your-32-byte-encryption-key" }, wantErr: []string{"ENCRYPTION_KEY", "placeholder"}},
		{name: "Orchestrator_PlaceholderDatabaseURL", service: ServiceOrchestrator, base: orchestratorOK,
			mutate: func(c *Config) {
				c.Database.URL = "postgres://<YOUR_DB_USER>:<YOUR_DB_PASSWORD>@postgres:5432/<YOUR_DB_NAME>"
			}, wantErr: []string{"DATABASE_URL", "placeholder"}},
		{name: "Orchestrator_TwilioSIDWithoutToken", service: ServiceOrchestrator, base: orchestratorOK,
			mutate: func(c *Config) { c.Twilio.AccountSID = "AC1" }, wantErr: []string{"TWILIO_AUTH_TOKEN"}},

		// --- proxy ---
		{name: "Proxy_OK", service: ServiceProxy,
			base: func() Config {
				return Config{Server: ServerConfig{GatewayURL: "http://gateway:8080", OrchestratorURL: "http://orchestrator:8081"}}
			}},
		{name: "Proxy_Missing", service: ServiceProxy, base: func() Config { return Config{} },
			wantErr: []string{"GATEWAY_URL", "ORCHESTRATOR_URL"}},

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

// Every key that Validate can demand must be documented in .env.example.
func TestEnvExampleListsEveryRequiredKey(t *testing.T) {
	content := readRepoFile(t, ".env.example")
	for _, key := range RequiredEnvKeys() {
		assert.Contains(t, content, key+"=", ".env.example must list %s", key)
	}
}
