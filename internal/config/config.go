package config

import (
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"github.com/nivik/mypa/internal/models"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Telegram TelegramConfig `mapstructure:"telegram"`
	RabbitMQ RabbitMQConfig `mapstructure:"rabbitmq"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Database DatabaseConfig `mapstructure:"database"`
	Gemini   GeminiConfig   `mapstructure:"gemini"`
	Google   GoogleConfig   `mapstructure:"google"`
	Groq     GroqConfig     `mapstructure:"groq"`
	Twilio   TwilioConfig   `mapstructure:"twilio"`
	Tavily   TavilyConfig   `mapstructure:"tavily"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	GatewayPort      int    `mapstructure:"gateway_port"`
	OrchestratorPort int    `mapstructure:"orchestrator_port"`
	ProxyPort        int    `mapstructure:"proxy_port"`
	GatewayURL       string `mapstructure:"gateway_url"`
	OrchestratorURL  string `mapstructure:"orchestrator_url"`
	DefaultTimezone  string `mapstructure:"default_timezone"`
	AllowedUsersRaw  string `mapstructure:"allowed_users"`
}

// TelegramConfig holds Telegram Bot API settings.
type TelegramConfig struct {
	BotToken   string `mapstructure:"bot_token"`
	WebhookURL string `mapstructure:"webhook_url"`
}

// RabbitMQConfig holds RabbitMQ connection settings.
type RabbitMQConfig struct {
	URL string `mapstructure:"url"`
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

// TwilioConfig holds Twilio WhatsApp API settings.
type TwilioConfig struct {
	AccountSID string `mapstructure:"account_sid"`
	AuthToken  string `mapstructure:"auth_token"`
	FromNumber string `mapstructure:"from_number"` // e.g., "whatsapp:+14155238886"
}

// Load reads configuration from config file and environment variables.
func Load() (*Config, error) {
	// Load .env file if it exists so Viper can read the env vars
	_ = godotenv.Load()

	v := viper.New()

	// Defaults
	v.SetDefault("server.gateway_port", 8080)
	v.SetDefault("server.orchestrator_port", 8081)
	v.SetDefault("server.proxy_port", 8000)
	v.SetDefault("server.default_timezone", "UTC")
	v.SetDefault("gemini.model", "gemini-2.5-flash")

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
		"DEFAULT_TIMEZONE":      "server.default_timezone",
		"ALLOWED_USERS":         "server.allowed_users",
		"GATEWAY_URL":           "server.gateway_url",
		"ORCHESTRATOR_URL":      "server.orchestrator_url",
		"TELEGRAM_BOT_TOKEN":    "telegram.bot_token",
		"TELEGRAM_WEBHOOK_URL":  "telegram.webhook_url",
		"RABBITMQ_URL":          "rabbitmq.url",
		"REDIS_URL":             "redis.url",
		"DATABASE_URL":          "database.url",
		"GEMINI_API_KEY":        "gemini.api_key",
		"GEMINI_MODEL":          "gemini.model",
		"GOOGLE_CLIENT_ID":      "google.client_id",
		"GOOGLE_CLIENT_SECRET":  "google.client_secret",
		"GOOGLE_REDIRECT_URL":   "google.redirect_url",
		"GROQ_API_KEY":          "groq.api_key",
		"TWILIO_ACCOUNT_SID":    "twilio.account_sid",
		"TWILIO_AUTH_TOKEN":     "twilio.auth_token",
		"TWILIO_FROM_NUMBER":    "twilio.from_number",
		"TAVILY_API_KEY":        "tavily.api_key",
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
