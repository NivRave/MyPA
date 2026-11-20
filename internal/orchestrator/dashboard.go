package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// Dashboard sign-in in chat (V4-35, ADR 0002 Decision 5). Like linking, these are commands
// handled before the model runs, never tools, so text the model reads cannot ask for a link.
//
//	/dashboard  in a direct chat: a sign-in link valid 10 minutes, for an owner, an adult or the
//	            operator, from an account older than 24 hours
//	/signout    ends every dashboard session of the person and voids an unused link
//
// The link carries a 256-bit token after `#`, which browsers never send to a server, so a chat
// app's link preview can neither use it nor log it. Only the token's SHA-256 is stored; the token
// and the link are never logged and never recorded in the audit session.

// SignInStore issues dashboard sign-in links and ends sessions. db.Client implements it.
type SignInStore interface {
	IssueSignIn(ctx context.Context, s identity.Scope, tokenHash string) (*models.AccessToken, error)
	RevokeSessionsOf(ctx context.Context, personID string) (int64, error)
}

// DefaultDashboardURL is the dashboard's address on a home install: its port is bound to this
// machine only (docker-compose.yml).
const DefaultDashboardURL = "http://localhost:3030"

// SignInPath is the dashboard page a sign-in link opens.
const SignInPath = "/signin"

// WithSignIn gives the engine the sign-in store and the dashboard's address (DASHBOARD_URL, or
// DefaultDashboardURL when empty). Without a store, /dashboard answers that sign-in is not set up.
func (e *Engine) WithSignIn(st SignInStore, dashboardURL string) *Engine {
	e.signIns = st
	e.dashboardURL = strings.TrimRight(strings.TrimSpace(dashboardURL), "/")
	if e.dashboardURL == "" {
		e.dashboardURL = DefaultDashboardURL
	}
	return e
}

// isLocalAddress reports whether the dashboard's address names this machine only.
func isLocalAddress(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// auditSignIn records a sign-in step in the turn's audit session (its tool summary): the purpose,
// the outcome and the token row, never the token or the link.
func auditSignIn(ctx context.Context, outcome, tokenID string) {
	args := map[string]interface{}{"purpose": "signin", "outcome": outcome}
	if tokenID != "" {
		args["token_id"] = tokenID
	}
	recordToolCall(ctx, "dashboard_signin", args, outcome)
}

// cmdDashboard answers "/dashboard": a sign-in link for this person.
func (e *Engine) cmdDashboard(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !isDirect(msg) {
		auditSignIn(ctx, "refused_group", "")
		return "", e.sendMessage(ctx, msg, "🔒 Ask for a dashboard link in a direct chat with me only, never in a group.")
	}
	scope := e.scopeOf(ctx, user)
	if !scope.Can(identity.SignInDashboard) {
		slog.Info("sign-in link refused", "purpose", "signin", "outcome", "role", "person_id", scope.PersonID(), "household_id", scope.HouseholdID(), "channel", msg.Channel)
		auditSignIn(ctx, "refused_role", "")
		return "", e.sendMessage(ctx, msg, "🚫 The dashboard is for the household's owners and adults. Ask an owner if you need something changed there.")
	}
	if e.signIns == nil || e.accounts == nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Dashboard sign-in is not set up on this install.")
	}
	acct, err := e.accounts.AccountOf(ctx, msg.Channel, msg.Sender)
	if err != nil || acct == nil {
		return "", e.sendMessage(ctx, msg, "❌ I could not find this account. Please try again.")
	}
	if !identity.MayIssue(acct.LinkedAt, time.Now()) {
		slog.Info("sign-in link refused", "purpose", "signin", "outcome", "account_too_new", "person_id", scope.PersonID(), "household_id", scope.HouseholdID(), "channel", msg.Channel)
		auditSignIn(ctx, "refused_account_too_new", "")
		return "", e.sendMessage(ctx, msg, "🔒 This account was added less than 24 hours ago, so it cannot open the dashboard yet. Use one of your older accounts.")
	}
	token, err := identity.NewSecret()
	if err != nil {
		return "", fmt.Errorf("new sign-in token: %w", err)
	}
	markSideEffect(ctx) // a link was issued (and any older one voided): never issue twice
	tok, err := e.signIns.IssueSignIn(ctx, scope, identity.HashSecret(token))
	if err != nil {
		slog.Error("sign-in link not issued", "person_id", scope.PersonID(), "error", err)
		return "", e.sendMessage(ctx, msg, "❌ I could not make a sign-in link. Please try again.")
	}
	slog.Info("sign-in link issued", "purpose", "signin", "outcome", "issued", "token_id", tok.ID, "person_id", scope.PersonID(), "household_id", scope.HouseholdID(), "channel", msg.Channel)
	auditSignIn(ctx, "issued", tok.ID)

	var sb strings.Builder
	if isLocalAddress(e.dashboardURL) {
		sb.WriteString("🖥️ Open this link in a browser on the computer MyPA runs on; it does not open anywhere else.\n\n")
	}
	sb.WriteString("🔑 Your dashboard sign-in link, valid for 10 minutes and once:\n\n")
	sb.WriteString(e.dashboardURL + SignInPath + "#" + token)
	sb.WriteString("\n\nNever send it to anyone: whoever opens it first is signed in as you. Send /signout to end every dashboard session.")
	return "", e.sendMessage(ctx, msg, sb.String())
}

// cmdSignout answers "/signout": every dashboard session of the person ends.
func (e *Engine) cmdSignout(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	scope := e.scopeOf(ctx, user)
	if e.signIns == nil || scope.PersonID() == "" {
		return "", e.sendMessage(ctx, msg, "⚠️ Dashboard sign-in is not set up on this install.")
	}
	markSideEffect(ctx)
	n, err := e.signIns.RevokeSessionsOf(ctx, scope.PersonID())
	if err != nil {
		slog.Error("dashboard sessions not ended", "person_id", scope.PersonID(), "error", err)
		return "", e.sendMessage(ctx, msg, "❌ I could not end your dashboard sessions. Please try again.")
	}
	slog.Info("dashboard sessions ended", "purpose", "signin", "outcome", "signed_out", "sessions", n, "person_id", scope.PersonID(), "channel", msg.Channel)
	auditSignIn(ctx, "signed_out", "")
	if n == 0 {
		return "", e.sendMessage(ctx, msg, "✅ You had no open dashboard session. Any unused sign-in link no longer works.")
	}
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("✅ Signed out of the dashboard everywhere (%d session(s) ended).", n))
}
