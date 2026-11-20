package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Dashboard sign-in exchange (V4-35, ADR 0002 Decision 5). The dashboard's server calls it when
// the person presses the button on the page a /dashboard link opened:
//
//	POST /auth/dashboard/signin   {"token": "<the token after # in the link>"}
//	→ 200 {"session": "<secret>", "expiresAt": "...", "maxAgeSeconds": 604800}
//
// The token is used in one statement and becomes a session; the dashboard puts the session secret
// in an HttpOnly cookie and sends it back as a bearer value on every /graphql request. Only the
// hashes of both are stored. Every refusal gets the same answer, so a caller learns nothing about
// a token it does not hold.

// SignInPath is the app's sign-in exchange.
const SignInPath = "/auth/dashboard/signin"

// signInBodyLimit is the most a sign-in request may carry: one 43-character token in JSON.
const signInBodyLimit = 4 << 10

// signInStore turns a sign-in token into a session. db.Client implements it.
type signInStore interface {
	RedeemSignIn(ctx context.Context, tokenHash, sessionHash, remoteAddress string) (*models.AccessToken, *models.Session, error)
	RevokeSession(ctx context.Context, sessionID string) error
	UserByID(ctx context.Context, id string) (*models.User, error)
}

type signInResponse struct {
	Session       string    `json:"session"`
	ExpiresAt     time.Time `json:"expiresAt"`
	MaxAgeSeconds int       `json:"maxAgeSeconds"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuseSignIn(w http.ResponseWriter, status int, reason string) {
	slog.Info("dashboard sign-in refused", "purpose", "signin", "outcome", "refused", "reason", reason)
	writeJSON(w, status, map[string]string{"error": "This sign-in link does not work any more. Ask for a new one with /dashboard in a direct chat with the assistant."})
}

// newSignInHandler serves SignInPath. households builds the person's scope, so a person who can
// no longer sign in (demoted or removed since the link was issued) gets no session; notifier tells
// the person in chat that a session started. The session row is the sign-in's audit record, with
// the remote address; nothing goes to the mutation log, which holds undoable changes only (a
// sign-in there would be the household's newest change and block /undo).
func newSignInHandler(st signInStore, households identity.Store, notifier AuthNotifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			refuseSignIn(w, http.StatusMethodNotAllowed, "method")
			return
		}
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			refuseSignIn(w, http.StatusUnsupportedMediaType, "content type")
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, signInBodyLimit))
		if err != nil || json.Unmarshal(raw, &body) != nil || !identity.WellFormedSecret(body.Token) {
			refuseSignIn(w, http.StatusBadRequest, "malformed")
			return
		}
		if st == nil || households == nil {
			refuseSignIn(w, http.StatusServiceUnavailable, "not configured")
			return
		}
		ctx := r.Context()
		secret, err := identity.NewSecret()
		if err != nil {
			refuseSignIn(w, http.StatusInternalServerError, "no randomness")
			return
		}
		tok, sess, err := st.RedeemSignIn(ctx, identity.HashSecret(body.Token), identity.HashSecret(secret), remoteAddress(r))
		if err != nil {
			if !errors.Is(err, identity.ErrSignInInvalid) {
				slog.Error("dashboard sign-in failed", "error", err)
			}
			refuseSignIn(w, http.StatusUnauthorized, "invalid, used or expired")
			return
		}
		u, err := st.UserByID(ctx, sess.PersonID)
		var scope identity.Scope
		if err == nil && u != nil {
			scope, err = identity.ForUser(ctx, households, u)
		}
		if u == nil || err != nil || !scope.Can(identity.SignInDashboard) {
			_ = st.RevokeSession(ctx, sess.ID)
			refuseSignIn(w, http.StatusForbidden, "the person may no longer sign in")
			return
		}

		slog.Info("dashboard session started", "purpose", "signin", "outcome", "used", "token_id", tok.ID,
			"person_id", scope.PersonID(), "household_id", scope.HouseholdID())
		if notifier != nil {
			if err := notifier.SendMessage(ctx, u.PlatformID, "🔐 You are now signed in to the MyPA dashboard. If that was not you, send /signout here."); err != nil {
				slog.Warn("dashboard sign-in not announced in chat", "person_id", scope.PersonID(), "error", err)
			}
		}
		writeJSON(w, http.StatusOK, signInResponse{Session: secret, ExpiresAt: sess.ExpiresAt, MaxAgeSeconds: int(identity.SessionMax / time.Second)})
	}
}

// remoteAddress is the browser's address as the dashboard's server passed it (X-Forwarded-For,
// first entry), else the connection's. It is informational only: it goes on the session row.
func remoteAddress(r *http.Request) string {
	if f := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); f != "" {
		return f
	}
	return r.RemoteAddr
}

// signInRecordKeep is how long ended sessions (the sign-ins' audit records) and used sign-in links
// are kept before the hourly clean-up deletes them.
const signInRecordKeep = 30 * 24 * time.Hour

// purgeSignInsEvery deletes ended sessions and used sign-in links once an hour, keeping
// signInRecordKeep of them, until stop is closed (the clean-up ADR 0002 Decision 5 asks for).
func purgeSignInsEvery(purge func(ctx context.Context, keep time.Duration) (int64, error), stop <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if n, err := purge(context.Background(), signInRecordKeep); err != nil {
				slog.Warn("could not purge ended dashboard sessions", "error", err)
			} else if n > 0 {
				slog.Info("purged ended dashboard sessions and used sign-in links", "rows", n)
			}
		}
	}
}
