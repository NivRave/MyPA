package config

import (
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Telegram TelegramConfig `mapstructure:"telegram"`
	RabbitMQ RabbitMQConfig `mapstructure:"rabbitmq"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Gemini   GeminiConfig   `mapstructure:"gemini"`
	Google   GoogleConfig   `mapstructure:"google"`
	Groq     GroqConfig     `mapstructure:"groq"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	GatewayPort      int    `mapstructure:"gateway_port"`
	OrchestratorPort int    `mapstructure:"orchestrator_port"`
	DefaultTimezone  string `mapstructure:"default_timezone"`
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

// GroqConfig holds Groq API settings.
type GroqConfig struct {
	APIKey string `mapstructure:"api_key"`
}

// Load reads configuration from config file and environment variables.
func Load() (*Config, error) {
	// Load .env file if it exists so Viper can read the env vars
	_ = godotenv.Load()

	v := viper.New()

	// Defaults
	v.SetDefault("server.gateway_port", 8080)
	v.SetDefault("server.orchestrator_port", 8081)
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
		"TELEGRAM_BOT_TOKEN":    "telegram.bot_token",
		"TELEGRAM_WEBHOOK_URL":  "telegram.webhook_url",
		"RABBITMQ_URL":          "rabbitmq.url",
		"REDIS_URL":             "redis.url",
		"GEMINI_API_KEY":        "gemini.api_key",
		"GEMINI_MODEL":          "gemini.model",
		"GOOGLE_CLIENT_ID":      "google.client_id",
		"GOOGLE_CLIENT_SECRET":  "google.client_secret",
		"GOOGLE_REDIRECT_URL":   "google.redirect_url",
		"GROQ_API_KEY":          "groq.api_key",
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
