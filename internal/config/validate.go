package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Service names accepted by Validate. Each cmd/<service>/main.go validates its own config at startup.
const (
	ServiceGateway      = "gateway"
	ServiceOrchestrator = "orchestrator"
	ServiceProxy        = "proxy"
	ServiceAuditWorker  = "audit-worker"
)

// requiredKeys lists the env vars each service cannot start without (conditional rules are in Validate).
var requiredKeys = map[string][]string{
	ServiceGateway:      {"RABBITMQ_URL"},
	ServiceOrchestrator: {"ENCRYPTION_KEY", "JWT_SECRET", "DASHBOARD_API_SECRET", "DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "GEMINI_API_KEY"},
	ServiceProxy:        {"GATEWAY_URL", "ORCHESTRATOR_URL"},
	ServiceAuditWorker:  {"DATABASE_URL", "RABBITMQ_URL"},
}

// conditionalKeys are required only when a related feature is configured.
var conditionalKeys = []string{"TELEGRAM_SECRET_TOKEN", "TELEGRAM_BOT_TOKEN", "TWILIO_AUTH_TOKEN"}

// RequiredEnvKeys returns every env var Validate may demand for any service (sorted, de-duplicated).
func RequiredEnvKeys() []string {
	seen := map[string]bool{}
	for _, keys := range requiredKeys {
		for _, k := range keys {
			seen[k] = true
		}
	}
	for _, k := range conditionalKeys {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// telegramSecretPattern: Telegram's secret_token allows 1-256 characters from A-Z, a-z, 0-9, _ and -.
var telegramSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// isPlaceholder detects values copied verbatim from .env.example.
func isPlaceholder(v string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "your-") || strings.Contains(v, "<YOUR_")
}

// Validate checks that everything the given service needs is present and sane.
// It returns all problems at once (errors.Join) so operators can fix them in one pass.
func (c *Config) Validate(service string) error {
	keys, ok := requiredKeys[service]
	if !ok {
		return fmt.Errorf("config: unknown service %q", service)
	}

	var errs []error
	for _, key := range keys {
		val, known := c.valueFor(key)
		switch {
		case !known:
			errs = append(errs, fmt.Errorf("config: no mapping for required key %s (programming error)", key))
		case strings.TrimSpace(val) == "":
			errs = append(errs, fmt.Errorf("%s is required for %s", key, service))
		case isPlaceholder(val):
			errs = append(errs, fmt.Errorf("%s still contains the .env.example placeholder value", key))
		}
	}

	switch service {
	case ServiceGateway:
		errs = append(errs, c.validateTelegramIngress()...)
		errs = append(errs, c.validateTwilio()...)
	case ServiceOrchestrator:
		if c.Server.JWTSecret != "" && strings.TrimSpace(c.Server.JWTSecret) == strings.TrimSpace(c.Encryption.Key) {
			errs = append(errs, errors.New("JWT_SECRET must differ from ENCRYPTION_KEY"))
		}
		errs = append(errs, c.validateTwilio()...)
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid %s configuration: %w", service, errors.Join(errs...))
}

func (c *Config) validateTelegramIngress() []error {
	var errs []error
	tg := c.Telegram
	if (tg.BotToken != "" || tg.WebhookURL != "") && tg.SecretToken == "" {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN is required when Telegram is configured (TELEGRAM_BOT_TOKEN/TELEGRAM_WEBHOOK_URL set)"))
	}
	if tg.SecretToken != "" && !telegramSecretPattern.MatchString(tg.SecretToken) {
		errs = append(errs, errors.New("TELEGRAM_SECRET_TOKEN must be 1-256 characters of A-Z, a-z, 0-9, '_' or '-'"))
	}
	if tg.WebhookURL != "" && tg.BotToken == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN is required to register TELEGRAM_WEBHOOK_URL"))
	}
	return errs
}

func (c *Config) validateTwilio() []error {
	if c.Twilio.AccountSID != "" && c.Twilio.AuthToken == "" {
		return []error{errors.New("TWILIO_AUTH_TOKEN is required when TWILIO_ACCOUNT_SID is set (webhook signatures cannot be verified)")}
	}
	return nil
}

// valueFor maps a required env var name to its loaded config value.
func (c *Config) valueFor(envKey string) (string, bool) {
	switch envKey {
	case "RABBITMQ_URL":
		return c.RabbitMQ.URL, true
	case "REDIS_URL":
		return c.Redis.URL, true
	case "DATABASE_URL":
		return c.Database.URL, true
	case "ENCRYPTION_KEY":
		return c.Encryption.Key, true
	case "JWT_SECRET":
		return c.Server.JWTSecret, true
	case "DASHBOARD_API_SECRET":
		return c.Server.DashboardAPISecret, true
	case "GEMINI_API_KEY":
		return c.Gemini.APIKey, true
	case "GATEWAY_URL":
		return c.Server.GatewayURL, true
	case "ORCHESTRATOR_URL":
		return c.Server.OrchestratorURL, true
	}
	return "", false
}
