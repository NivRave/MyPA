package orchestrator

import (
	"context"
	"github.com/nivik/mypa/internal/identity"
	"log/slog"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
)

// HealthReporter produces the `/doctor` report (V4-17) for an admin, as Markdown. The engine
// renders it for the channel like any other reply. The implementation is internal/doctor; the
// engine only knows this port.
type HealthReporter interface {
	// Report runs the health checks on behalf of admin. Checks about people are limited to the
	// admin's own family group.
	Report(ctx context.Context, admin *models.User) string
}

// WithHealthReporter sets what `/doctor` reports. Without one the command says it is unavailable.
func (e *Engine) WithHealthReporter(h HealthReporter) *Engine {
	e.health = h
	return e
}

// cmdDoctor answers `/doctor`. Admins only: the report describes the install and names the
// members of the admin's family. It only reads, so it marks no side effect, and nothing is
// recorded as the turn's final response (the report is not copied into the audit log).
func (e *Engine) cmdDoctor(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !e.scopeOf(ctx, user).Can(identity.RunDoctor) {
		slog.Warn("unauthorized doctor command attempt", "user_id", user.PlatformID, "role", e.scopeOf(ctx, user).Role())
		return "", e.sendMessage(ctx, msg, "🚫 Permission denied: administrative privileges required.")
	}
	if e.health == nil {
		return "", e.sendMessage(ctx, msg, "⚠️ The health check is not available on this install.")
	}
	return "", e.sendMessage(ctx, msg, e.health.Report(ctx, user))
}
