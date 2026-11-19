package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nivik/mypa/internal/channel/adapters"
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

// conditionalKeys are required only when a related feature is configured: the channel adapters'
// settings (V4-31), which the adapters validate themselves.
var conditionalKeys = adapters.ConditionalEnvKeys()

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

	servesIngress := service == RoleAll || service == RoleIngress
	runsWorker := service == RoleAll || service == RoleWorker
	if runsWorker {
		if c.Server.JWTSecret != "" && strings.TrimSpace(c.Server.JWTSecret) == strings.TrimSpace(c.Encryption.Key) {
			errs = append(errs, errors.New("JWT_SECRET must differ from ENCRYPTION_KEY"))
		}
	}
	// The audit worker runs no channel, so it does not check their settings.
	errs = append(errs, c.Channels.Validate(servesIngress, runsWorker)...)

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid %s configuration: %w", service, errors.Join(errs...))
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
