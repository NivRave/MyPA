package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/nivik/mypa/internal/channel/adapters"
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
	Google     GoogleConfig     `mapstructure:"google"`
	Groq       GroqConfig       `mapstructure:"groq"`
	Tavily     TavilyConfig     `mapstructure:"tavily"`
	Encryption EncryptionConfig `mapstructure:"encryption"`
	// Channels holds the channel adapters' own settings (V4-31). Each adapter reads and checks its
	// settings itself, so this package does not know which channels exist.
	Channels adapters.Config `mapstructure:"-"`
}

// EncryptionConfig holds cryptographic encryption settings.
type EncryptionConfig struct {
	Key string `mapstructure:"key"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the one HTTP port of the app (V4-01), whatever its role. HTTP_PORT, default 8000.
	Port               int    `mapstructure:"port"`
	DefaultTimezone    string `mapstructure:"default_timezone"`
	AllowedUsersRaw    string `mapstructure:"allowed_users"`
	DashboardAPISecret string `mapstructure:"dashboard_api_secret"`
	// DashboardFamilyGroup scopes the dashboard secret to one family group (P3-2).
	DashboardFamilyGroup string `mapstructure:"dashboard_family_group"`
	JWTSecret            string `mapstructure:"jwt_secret"`
	WeekStartDay         string `mapstructure:"week_start_day"` // e.g. "Sunday" (default) or "Monday"
	WeekendDays          string `mapstructure:"weekend_days"`   // e.g. "Friday,Saturday" (default)
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

// GoogleConfig holds Google OAuth settings for Calendar API.
type GoogleConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURL  string `mapstructure:"redirect_url"`
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
		"DEFAULT_TIMEZONE":       "server.default_timezone",
		"ALLOWED_USERS":          "server.allowed_users",
		"HTTP_PORT":              "server.port",
		"RABBITMQ_URL":           "rabbitmq.url",
		"RABBITMQ_PREFETCH":      "rabbitmq.prefetch",
		"REDIS_URL":              "redis.url",
		"DATABASE_URL":           "database.url",
		"GEMINI_API_KEY":         "gemini.api_key",
		"GEMINI_MODEL":           "gemini.model",
		"GOOGLE_CLIENT_ID":       "google.client_id",
		"GOOGLE_CLIENT_SECRET":   "google.client_secret",
		"GOOGLE_REDIRECT_URL":    "google.redirect_url",
		"GROQ_API_KEY":           "groq.api_key",
		"TAVILY_API_KEY":         "tavily.api_key",
		"ENCRYPTION_KEY":         "encryption.key",
		"DASHBOARD_API_SECRET":   "server.dashboard_api_secret",
		"DASHBOARD_FAMILY_GROUP": "server.dashboard_family_group",
		"JWT_SECRET":             "server.jwt_secret",
		"WEEK_START_DAY":         "server.week_start_day",
		"WEEKEND_DAYS":           "server.weekend_days",
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

	return &cfg, nil
}

// ParseAllowedUsers parses the ALLOWED_USERS string into a map keyed by platform ID.
// Format: "phone:name:role:family_group, phone2:name2:role2:family_group2"
func (c *Config) ParseAllowedUsers() map[string]models.User {
	users := make(map[string]models.User)
	if c.Server.AllowedUsersRaw == "" {
		return users
	}

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

		if u.PlatformID != "" {
			users[u.PlatformID] = u
		}
	}
	return users
}
