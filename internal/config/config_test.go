package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearEnvKeepTemp empties the process environment for one test, so that Load sees only what that
// test sets, and puts the whole environment back when the test ends. The environment belongs to the
// process, not to the test: without the restore every later test in the package ran without PATH,
// and the guards that need git skipped themselves in every full run (BL-27).
func clearEnvKeepTemp(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	t.Cleanup(func() { restoreEnv(t, saved) }) // registered first, so it runs after t.Setenv's own cleanups

	kept := map[string]string{}
	for _, key := range []string{"TMP", "TEMP", "USERPROFILE"} {
		kept[key] = os.Getenv(key)
	}
	os.Clearenv()
	for key, value := range kept {
		t.Setenv(key, value) // t.Setenv also refuses to run in a parallel test
	}
}

// restoreEnv makes the process environment equal to saved, a snapshot from os.Environ.
func restoreEnv(t *testing.T, saved []string) {
	t.Helper()
	os.Clearenv()
	for _, entry := range saved {
		if entry == "" {
			continue
		}
		// On Windows a name may itself begin with "=" (the per-drive directories, "=C:"), so the
		// separator is the first "=" after the first byte. os.Clearenv splits the same way.
		rest, value, ok := strings.Cut(entry[1:], "=")
		if !ok {
			continue
		}
		if err := os.Setenv(entry[:1]+rest, value); err != nil {
			t.Errorf("restoring the environment: %s: %v", entry[:1]+rest, err)
		}
	}
}

// BL-27: a test that clears the environment must hand it back complete, PATH included.
func TestClearEnvKeepTemp_RestoresTheEnvironment(t *testing.T) {
	t.Setenv("BL27_MARKER", "set before the clear")
	before := os.Environ()
	require.NotEmpty(t, os.Getenv("PATH"), "the test process itself must have a PATH")

	t.Run("cleared inside", func(t *testing.T) {
		clearEnvKeepTemp(t)
		assert.Empty(t, os.Getenv("PATH"))
		assert.Empty(t, os.Getenv("BL27_MARKER"))
		t.Setenv("BL27_SET_INSIDE", "must not leak out")
	})

	assert.ElementsMatch(t, before, os.Environ(), "the environment after the test differs from the one before it")
	assert.Equal(t, "set before the clear", os.Getenv("BL27_MARKER"))
	_, leaked := os.LookupEnv("BL27_SET_INSIDE")
	assert.False(t, leaked)
}

func TestLoad_Defaults(t *testing.T) {
	clearEnvKeepTemp(t)

	tempDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, 8080, cfg.Server.GatewayPort)
	assert.Equal(t, 8081, cfg.Server.OrchestratorPort)
	assert.Equal(t, 8000, cfg.Server.ProxyPort)
	assert.Equal(t, "UTC", cfg.Server.DefaultTimezone)
	assert.Equal(t, DefaultGeminiModel, cfg.Gemini.Model)
	assert.Equal(t, "Sunday", cfg.Server.WeekStartDay)
	assert.Equal(t, "Friday,Saturday", cfg.Server.WeekendDays)
	assert.Equal(t, 4, cfg.RabbitMQ.Prefetch)
}

func TestLoad_EnvVars(t *testing.T) {
	clearEnvKeepTemp(t)

	tempDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	t.Setenv("DEFAULT_TIMEZONE", "America/New_York")
	t.Setenv("GATEWAY_URL", "http://localhost:8080")
	t.Setenv("ORCHESTRATOR_URL", "http://localhost:8081")
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("RABBITMQ_URL", "amqp://user:pass@localhost:5672/")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("GEMINI_MODEL", "gemini-1.5-pro")
	t.Setenv("GOOGLE_CLIENT_ID", "google-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "google-client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost/callback")
	t.Setenv("GROQ_API_KEY", "groq-key")
	t.Setenv("TWILIO_ACCOUNT_SID", "twilio-sid")
	t.Setenv("TWILIO_AUTH_TOKEN", "twilio-token")
	t.Setenv("TWILIO_FROM_NUMBER", "whatsapp:+1234567890")
	t.Setenv("TWILIO_STATUS_CALLBACK_URL", "https://example.com/webhook/twilio/status")
	t.Setenv("TAVILY_API_KEY", "tavily-key")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "https://example.com/webhook/twilio/status", cfg.Twilio.StatusCallbackURL)
	assert.Equal(t, "America/New_York", cfg.Server.DefaultTimezone)
	assert.Equal(t, "http://localhost:8080", cfg.Server.GatewayURL)
	assert.Equal(t, "http://localhost:8081", cfg.Server.OrchestratorURL)
	assert.Equal(t, "test-token", cfg.Telegram.BotToken)
	assert.Equal(t, "https://example.com/webhook", cfg.Telegram.WebhookURL)
	assert.Equal(t, "amqp://user:pass@localhost:5672/", cfg.RabbitMQ.URL)
	assert.Equal(t, "redis://localhost:6379/0", cfg.Redis.URL)
	assert.Equal(t, "postgres://user:pass@localhost:5432/db", cfg.Database.URL)
	assert.Equal(t, "gemini-key", cfg.Gemini.APIKey)
	assert.Equal(t, "gemini-1.5-pro", cfg.Gemini.Model)
	assert.Equal(t, "google-client-id", cfg.Google.ClientID)
	assert.Equal(t, "google-client-secret", cfg.Google.ClientSecret)
	assert.Equal(t, "http://localhost/callback", cfg.Google.RedirectURL)
	assert.Equal(t, "groq-key", cfg.Groq.APIKey)
	assert.Equal(t, "twilio-sid", cfg.Twilio.AccountSID)
	assert.Equal(t, "twilio-token", cfg.Twilio.AuthToken)
	assert.Equal(t, "whatsapp:+1234567890", cfg.Twilio.FromNumber)
	assert.Equal(t, "tavily-key", cfg.Tavily.APIKey)
}

func TestLoad_ConfigFile(t *testing.T) {
	clearEnvKeepTemp(t)

	tempDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	configContent := `
server:
  gateway_port: 9090
  default_timezone: "Europe/London"
gemini:
  model: "gemini-test"
`
	err = os.WriteFile(filepath.Join(tempDir, "config.yaml"), []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, 9090, cfg.Server.GatewayPort)
	assert.Equal(t, "Europe/London", cfg.Server.DefaultTimezone)
	assert.Equal(t, "gemini-test", cfg.Gemini.Model)

	assert.Equal(t, 8081, cfg.Server.OrchestratorPort)
}
