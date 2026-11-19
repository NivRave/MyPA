package telegram

import (
	"errors"
	"regexp"

	"github.com/nivik/mypa/internal/deploy"
)

// Config holds the Telegram Bot API settings. It was config.TelegramConfig before the channel
// layer (V4-31) made each adapter own its settings.
type Config struct {
	BotToken    string // TELEGRAM_BOT_TOKEN
	SecretToken string // TELEGRAM_SECRET_TOKEN: Telegram sends it with every update; the webhook fails closed without it
	// WebhookURL is where Telegram delivers updates: PUBLIC_URL followed by /webhook/telegram
	// (V4-40). It is derived, never set on its own, and empty when the install has no public
	// address. Registered at start.
	WebhookURL string
	// Polling: the install has no public address (INGRESS=none), so the adapter fetches updates
	// itself and serves no webhook route (V4-40). The zero value is the webhook.
	Polling bool
}

// LoadConfig reads the settings from the environment (getenv is os.Getenv in the app). With a
// public address Telegram calls the webhook; without one the adapter polls.
func LoadConfig(getenv func(string) string, site deploy.Settings) Config {
	return Config{
		BotToken:    getenv("TELEGRAM_BOT_TOKEN"),
		SecretToken: getenv("TELEGRAM_SECRET_TOKEN"),
		WebhookURL:  site.URL(WebhookPath),
		Polling:     !site.Reachable(),
	}
}

// ConditionalEnvKeys are the settings Validate demands when Telegram is configured.
func ConditionalEnvKeys() []string { return []string{"TELEGRAM_SECRET_TOKEN", "TELEGRAM_BOT_TOKEN"} }

// ReplacedEnvKeys are settings this adapter no longer reads: PUBLIC_URL replaced them (V4-40).
func ReplacedEnvKeys() []string { return []string{"TELEGRAM_WEBHOOK_URL"} }

// secretPattern: Telegram's secret_token allows 1-256 characters from A-Z, a-z, 0-9, _ and -.
var secretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// Validate checks the settings for a process that does or does not receive messages. Only the
// ingress needs them checked: it is where the secret token is verified. A polling install serves
// no webhook, so it needs no secret token.
func (c Config) Validate(servesIngress bool) []error {
	if !servesIngress {
		return nil
	}
	var errs []error
	if !c.Polling && (c.BotToken != "" || c.WebhookURL != "") && c.SecretToken == "" {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN is required when Telegram receives webhooks (TELEGRAM_BOT_TOKEN set and PUBLIC_URL gives a public address)"))
	}
	if c.SecretToken != "" && !secretPattern.MatchString(c.SecretToken) {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN must be 1-256 characters of A-Z, a-z, 0-9, '_' or '-'"))
	}
	if c.WebhookURL != "" && c.BotToken == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN is required to register the Telegram webhook at PUBLIC_URL"))
	}
	return errs
}
