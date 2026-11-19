package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Names accepted by Validate. The app is one binary with roles (V4-01, `mypa --role=<role>`), and
// it validates the configuration of its role at startup; the audit worker validates its own.
const (
	// RoleAll runs the ingress and the worker in one process. It is the default.
	RoleAll = "all"
	// RoleIngress only receives the webhooks: it verifies, normalises and enqueues them.
	RoleIngress = "ingress"
	// RoleWorker runs the conversation turns, the tools, the scheduler, GraphQL and Google sign-in.
	RoleWorker = "worker"
	// ServiceAuditWorker is the separate audit worker (until V4-23 moves its queue).
	ServiceAuditWorker = "audit-worker"
)

// Roles returns the app roles, in the order `--role` documents them.
func Roles() []string { return []string{RoleAll, RoleIngress, RoleWorker} }

var (
	ingressKeys = []string{"RABBITMQ_URL"}
	workerKeys  = []string{"ENCRYPTION_KEY", "JWT_SECRET", "DASHBOARD_API_SECRET", "DASHBOARD_FAMILY_GROUP", "DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "GEMINI_API_KEY"}
)

// requiredKeys lists the env vars each role or service cannot start without (conditional rules are
// in Validate). RoleAll needs what the worker needs, which already includes everything the ingress needs.
var requiredKeys = map[string][]string{
	RoleAll:            workerKeys,
	RoleIngress:        ingressKeys,
	RoleWorker:         workerKeys,
	ServiceAuditWorker: {"DATABASE_URL", "RABBITMQ_URL"},
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

	if service == RoleAll || service == RoleIngress {
		errs = append(errs, c.validateTelegramIngress()...)
	}
	if service == RoleAll || service == RoleWorker {
		if c.Server.JWTSecret != "" && strings.TrimSpace(c.Server.JWTSecret) == strings.TrimSpace(c.Encryption.Key) {
			errs = append(errs, errors.New("JWT_SECRET must differ from ENCRYPTION_KEY"))
		}
		errs = append(errs, c.validateTwilioStatusCallback()...)
	}
	if service != ServiceAuditWorker {
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

// validateTwilioStatusCallback: optional (BL-8), but if set Twilio must be able to call it — an
// absolute https URL, not an .env.example placeholder.
func (c *Config) validateTwilioStatusCallback() []error {
	raw := strings.TrimSpace(c.Twilio.StatusCallbackURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return []error{errors.New("TWILIO_STATUS_CALLBACK_URL must be an absolute https URL (e.g. https://<public-host>/webhook/twilio/status)")}
	}
	if strings.Contains(strings.ToLower(raw), "your-") || strings.Contains(raw, "<YOUR_") {
		return []error{errors.New("TWILIO_STATUS_CALLBACK_URL still contains the .env.example placeholder value")}
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
	case "DASHBOARD_FAMILY_GROUP":
		return c.Server.DashboardFamilyGroup, true
	case "GEMINI_API_KEY":
		return c.Gemini.APIKey, true
	}
	return "", false
}
