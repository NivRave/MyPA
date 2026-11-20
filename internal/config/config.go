package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/deploy"
	"github.com/nivik/mypa/internal/models"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	RabbitMQ   RabbitMQConfig   `mapstructure:"rabbitmq"`
	Redis      RedisConfig      `mapstructure:"redis"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Gemini     GeminiConfig     `mapstructure:"gemini"`
	Embedding  EmbeddingConfig  `mapstructure:"embedding"`
	Google     GoogleConfig     `mapstructure:"google"`
	Groq       GroqConfig       `mapstructure:"groq"`
	Tavily     TavilyConfig     `mapstructure:"tavily"`
	Encryption EncryptionConfig `mapstructure:"encryption"`
	// Channels holds the channel adapters' own settings (V4-31). Each adapter reads and checks its
	// settings itself, so this package does not know which channels exist.
	Channels adapters.Config `mapstructure:"-"`
	// Deploy is how the install is reached: INGRESS and the one PUBLIC_URL every public address is
	// derived from (V4-40).
	Deploy deploy.Settings `mapstructure:"-"`

	// replaced lists the settings PUBLIC_URL replaced that are still set; Validate refuses them.
	replaced []string
	// retired lists the settings dashboard sign-in ended (V4-35) that are still set; Validate
	// refuses them.
	retired []string
}

// RetiredEnvKeys are the settings V4-35 ended: the shared dashboard secret, the family it was
// bound to, and the JWT signing secret. Dashboard access is a per-person sign-in from /dashboard
// in chat (ADR 0002 Decision 5); nothing reads these any more.
func RetiredEnvKeys() []string {
	return []string{"DASHBOARD_API_SECRET", "DASHBOARD_FAMILY_GROUP", "JWT_SECRET"}
}

// GoogleCallbackPath is where Google returns after a person signs in (the app's OAuth callback).
const GoogleCallbackPath = "/auth/google/callback"

// replacedEnvKeys are the settings PUBLIC_URL replaced (V4-40): the adapters' own, the Google
// redirect, and the ngrok domain the tunnel was started with.
func replacedEnvKeys() []string {
	return append(adapters.ReplacedEnvKeys(), "GOOGLE_REDIRECT_URL", "NGROK_DOMAIN")
}

// EncryptionConfig holds cryptographic encryption settings.
type EncryptionConfig struct {
	Key string `mapstructure:"key"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the one HTTP port of the app (V4-01), whatever its role. HTTP_PORT, default 8000.
	Port            int    `mapstructure:"port"`
	DefaultTimezone string `mapstructure:"default_timezone"`
	AllowedUsersRaw string `mapstructure:"allowed_users"`
	// DashboardURL is the dashboard's address, which /dashboard sign-in links open (V4-35).
	// Empty: http://localhost:3030, the port docker-compose.yml binds to this machine.
	DashboardURL string `mapstructure:"dashboard_url"`
	// LinkCodeSecret is the server secret typed codes (link codes, V4-32) are hashed under, kept
	// out of the database and backups (ADR 0002 Decision 5). Empty: linking accounts is refused.
	LinkCodeSecret string `mapstructure:"link_code_secret"`
	WeekStartDay   string `mapstructure:"week_start_day"` // e.g. "Sunday" (default) or "Monday"
	WeekendDays    string `mapstructure:"weekend_days"`   // e.g. "Friday,Saturday" (default)
}

// RabbitMQConfig holds RabbitMQ connection settings.
type RabbitMQConfig struct {
	URL      string `mapstructure:"url"`
	Prefetch int    `mapstructure:"prefetch"` // max deliveries in flight per consumer (P2-8)
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	URL string `mapstructure:"url"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL string `mapstructure:"url"`
}

// GeminiConfig holds Gemini API settings.
type GeminiConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
}

// EmbeddingConfig is the embedding model, a setting of its own (V4-03). It is never derived from
// the chat model: the stored memory vectors belong to the embedding model that made them, so a
// change of chat model or provider must leave them alone. The key is GEMINI_API_KEY, which every
// install needs for embeddings until a second embedding provider is supported, even one that
// chats through another provider.
type EmbeddingConfig struct {
	Model string `mapstructure:"model"`
}

// GoogleConfig holds Google OAuth settings for Calendar API.
type GoogleConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	// RedirectURL is PUBLIC_URL followed by GoogleCallbackPath; without a public address it is the
	// app's local port, which only a browser on the same machine can reach (V4-40). Derived, never
	// set on its own.
	RedirectURL string `mapstructure:"-"`
}

type TavilyConfig struct {
	APIKey string `mapstructure:"api_key"`
}

// GroqConfig holds Groq API settings.
type GroqConfig struct {
	APIKey string `mapstructure:"api_key"`
}

// DefaultGeminiModel is the chat model an install uses when GEMINI_MODEL is not set. It is the
// one place the default is defined (V4-09): the settings file shipped in the image, the settings
// template and the LLM client's own fallback all defer to it. Change the default here and nowhere
// else; TestDefaultChatModelIsDefinedInOnePlace fails if a chat model is named anywhere else in
// production code, configuration or the dashboard, by its API name or its display name.
const DefaultGeminiModel = "gemini-3.6-flash"

// DefaultEmbeddingModel is the embedding model an install uses when EMBEDDING_MODEL is not set
// (V4-03). It is the model every install has embedded its memories with so far. Changing it on an
// install with memories needs a re-embed (`mypa embeddings reembed`, docs/EMBEDDINGS.md): the app
// refuses to start while the configured model differs from the one the stored vectors came from.
const DefaultEmbeddingModel = "gemini-embedding-001"

// Load reads configuration from config file and environment variables.
func Load() (*Config, error) {
	// Load .env file if it exists so Viper can read the env vars
	_ = godotenv.Load()

	v := viper.New()

	// Defaults
	v.SetDefault("server.port", 8000)
	v.SetDefault("server.default_timezone", "UTC")
	v.SetDefault("server.week_start_day", "Sunday")
	v.SetDefault("rabbitmq.prefetch", 4)
	v.SetDefault("server.weekend_days", "Friday,Saturday")
	v.SetDefault("gemini.model", DefaultGeminiModel)
	v.SetDefault("embedding.model", DefaultEmbeddingModel)

	// Config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath("/config")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		// Config file not found is OK — we'll rely on env vars
	}

	// Environment variables
	v.SetEnvPrefix("")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicit env var bindings (maps FLAT env vars to nested config keys)
	envBindings := map[string]string{
		"DEFAULT_TIMEZONE":     "server.default_timezone",
		"ALLOWED_USERS":        "server.allowed_users",
		"HTTP_PORT":            "server.port",
		"RABBITMQ_URL":         "rabbitmq.url",
		"RABBITMQ_PREFETCH":    "rabbitmq.prefetch",
		"REDIS_URL":            "redis.url",
		"DATABASE_URL":         "database.url",
		"GEMINI_API_KEY":       "gemini.api_key",
		"GEMINI_MODEL":         "gemini.model",
		"EMBEDDING_MODEL":      "embedding.model",
		"GOOGLE_CLIENT_ID":     "google.client_id",
		"GOOGLE_CLIENT_SECRET": "google.client_secret",
		"GROQ_API_KEY":         "groq.api_key",
		"TAVILY_API_KEY":       "tavily.api_key",
		"ENCRYPTION_KEY":       "encryption.key",
		"DASHBOARD_URL":        "server.dashboard_url",
		"LINK_CODE_SECRET":     "server.link_code_secret",
		"WEEK_START_DAY":       "server.week_start_day",
		"WEEKEND_DAYS":         "server.weekend_days",
	}

	for envKey, configKey := range envBindings {
		if err := v.BindEnv(configKey, envKey); err != nil {
			return nil, fmt.Errorf("binding env var %s: %w", envKey, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	// The adapters read their own settings from the environment, which now includes .env.
	cfg.Channels = adapters.LoadConfig(os.Getenv)
	cfg.Deploy = deploy.Load(os.Getenv)
	cfg.Google.RedirectURL = cfg.Deploy.URL(GoogleCallbackPath)
	if cfg.Google.RedirectURL == "" {
		cfg.Google.RedirectURL = fmt.Sprintf("http://localhost:%d%s", cfg.Server.Port, GoogleCallbackPath)
	}
	for _, k := range replacedEnvKeys() {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			cfg.replaced = append(cfg.replaced, k)
		}
	}
	for _, k := range RetiredEnvKeys() {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			cfg.retired = append(cfg.retired, k)
		}
	}

	return &cfg, nil
}

// ParseAllowedUsers parses the ALLOWED_USERS string, in the order the entries are written (the
// bootstrap makes the first entry of a household without an admin its owner). A platform ID
// written twice keeps its first position and its last values.
// Format: "id:name:role:family_group[:channel], ..."
//
// Since V4-30 the setting seeds a fresh install only: it is read when no household exists
// (ADR 0002 Decision 6, db.BootstrapHouseholds). The optional fifth field names the entry's
// channel (V4-32); AllowedUserChannels returns it.
func (c *Config) ParseAllowedUsers() []models.User {
	var users []models.User
	if c.Server.AllowedUsersRaw == "" {
		return users
	}
	index := map[string]int{}

	entries := strings.Split(c.Server.AllowedUsersRaw, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.Split(entry, ":")
		u := models.User{}
		if len(parts) >= 1 {
			u.PlatformID = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			u.Name = strings.TrimSpace(parts[1])
		}
		if len(parts) >= 3 {
			u.Role = strings.TrimSpace(parts[2])
		}
		if len(parts) >= 4 {
			u.FamilyGroup = strings.TrimSpace(parts[3])
		}

		if u.PlatformID == "" {
			continue
		}
		if i, seen := index[u.PlatformID]; seen {
			users[i] = u
			continue
		}
		index[u.PlatformID] = len(users)
		users = append(users, u)
	}
	return users
}

// AllowedUserChannels returns the channel named in the optional fifth field of each ALLOWED_USERS
// entry, by platform ID (V4-32, ADR 0002 Decision 6). An entry without one is not listed: its
// channel is the one whose adapter recognizes its ID.
func (c *Config) AllowedUserChannels() map[string]string {
	out := map[string]string{}
	for _, entry := range strings.Split(c.Server.AllowedUsersRaw, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		if len(parts) < 5 {
			continue
		}
		id, ch := strings.TrimSpace(parts[0]), strings.ToLower(strings.TrimSpace(parts[4]))
		if id != "" && ch != "" {
			out[id] = ch
		}
	}
	return out
}
