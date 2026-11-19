package telegram

import (
	"errors"
	"regexp"
)

// Config holds the Telegram Bot API settings. It was config.TelegramConfig before the channel
// layer (V4-31) made each adapter own its settings.
type Config struct {
	BotToken    string // TELEGRAM_BOT_TOKEN
	WebhookURL  string // TELEGRAM_WEBHOOK_URL: the public address of /webhook/telegram, registered at start
	SecretToken string // TELEGRAM_SECRET_TOKEN: Telegram sends it with every update; the webhook fails closed without it
}

// LoadConfig reads the settings from the environment (getenv is os.Getenv in the app).
func LoadConfig(getenv func(string) string) Config {
	return Config{
		BotToken:    getenv("TELEGRAM_BOT_TOKEN"),
		WebhookURL:  getenv("TELEGRAM_WEBHOOK_URL"),
		SecretToken: getenv("TELEGRAM_SECRET_TOKEN"),
	}
}

// ConditionalEnvKeys are the settings Validate demands when Telegram is configured.
func ConditionalEnvKeys() []string { return []string{"TELEGRAM_SECRET_TOKEN", "TELEGRAM_BOT_TOKEN"} }

// secretPattern: Telegram's secret_token allows 1-256 characters from A-Z, a-z, 0-9, _ and -.
var secretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// Validate checks the settings for a process that does or does not receive webhooks. Only the
// ingress needs them checked: it is where the secret token is verified.
func (c Config) Validate(servesIngress bool) []error {
	if !servesIngress {
		return nil
	}
	var errs []error
	if (c.BotToken != "" || c.WebhookURL != "") && c.SecretToken == "" {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN is required when Telegram is configured (TELEGRAM_BOT_TOKEN/TELEGRAM_WEBHOOK_URL set)"))
	}
	if c.SecretToken != "" && !secretPattern.MatchString(c.SecretToken) {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN must be 1-256 characters of A-Z, a-z, 0-9, '_' or '-'"))
	}
	if c.WebhookURL != "" && c.BotToken == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN is required to register TELEGRAM_WEBHOOK_URL"))
	}
	return errs
}
