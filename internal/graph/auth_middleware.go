package graph

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Dashboard authentication (V4-35, ADR 0002 Decision 5). The only credential /graphql accepts is a
// dashboard session: the browser holds a random secret in an HttpOnly cookie on the dashboard's
// origin, and the dashboard's server sends it here as `Authorization: Bearer <secret>`. Only the
// secret's SHA-256 is stored. The shared dashboard secret, DASHBOARD_FAMILY_GROUP and signed JWTs
// are gone: nothing else grants access.
//
// The person's role and household are read from their membership on every request, never taken
// from the session or the request, so a demotion or a removal takes effect at once.

// SessionStore finds the person behind a session. db.Client implements it.
type SessionStore interface {
	// SessionPerson returns the session and its person for a secret's hash, recording the
	// request, or identity.ErrNoSession.
	SessionPerson(ctx context.Context, sessionHash string) (sessionID, personID string, err error)
	// UserByID returns the users row of a person (a person keeps users.id), or nil.
	UserByID(ctx context.Context, id string) (*models.User, error)
}

type callerKey struct{}

// caller is who a request acts as: the signed-in person's scope, and the session it came with.
type caller struct {
	scope     identity.Scope
	sessionID string
}

// WithCaller attaches the signed-in person's scope to ctx. The middleware is its only production
// caller; tests use it to act as a member.
func WithCaller(ctx context.Context, s identity.Scope) context.Context {
	return context.WithValue(ctx, callerKey{}, caller{scope: s})
}

func withSession(ctx context.Context, s identity.Scope, sessionID string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller{scope: s, sessionID: sessionID})
}

func callerOf(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(callerKey{}).(caller)
	return c, ok && c.scope.Valid()
}

func unauthorized(w http.ResponseWriter, reason string) {
	slog.Info("dashboard request refused", "reason", reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized: sign in with /dashboard in a direct chat with the assistant"}]}`))
}

// NewAuthMiddleware authenticates /graphql by dashboard session. Without a store it refuses every
// request (fail closed).
func NewAuthMiddleware(sessions SessionStore, households identity.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				unauthorized(w, "no session")
				return
			}
			secret := strings.TrimSpace(parts[1])
			if !identity.WellFormedSecret(secret) {
				unauthorized(w, "malformed session")
				return
			}
			if sessions == nil || households == nil {
				unauthorized(w, "sign-in not configured")
				return
			}
			ctx := r.Context()
			sessionID, personID, err := sessions.SessionPerson(ctx, identity.HashSecret(secret))
			if err != nil {
				if !errors.Is(err, identity.ErrNoSession) {
					slog.Error("dashboard session lookup failed", "error", err)
				}
				unauthorized(w, "no valid session")
				return
			}
			u, err := sessions.UserByID(ctx, personID)
			if err != nil || u == nil {
				unauthorized(w, "the session's person is gone")
				return
			}
			s, err := identity.ForUser(ctx, households, u)
			if err != nil {
				unauthorized(w, "the session's person has no household")
				return
			}
			next.ServeHTTP(w, r.WithContext(withSession(ctx, s, sessionID)))
		})
	}
}
