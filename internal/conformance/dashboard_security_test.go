package conformance

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	layer "github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Attempts to get or leak a dashboard sign-in link (ADR 0002 Decision 5), on the suite's install:
// the real adapters, the real engine, an in-memory sign-in store.

// A link is shown in a direct chat only, never in a group.
func TestDashboard_DirectChatsOnly(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "adult", "Levi")
	u, _ := e.db.user(p.UserID)
	group := layer.Conversation{ID: "-100300", Kind: layer.Group, Addressed: true}

	e.process(layer.Inbound{Channel: telegram.Name, MessageID: "g1", Sender: p.UserID, Conversation: group, Text: "/dashboard"})
	assert.Contains(t, e.lastText(), "direct chat")
	assert.Empty(t, tokenIn(strings.Join(e.texts(), "\n")), "no link is shown in a group")
	assert.Empty(t, e.db.signInTokens(u.ID), "and none is issued")
}

// A newly added account cannot ask for a link for 24 hours (Decision 4: a stolen, unlocked phone
// that adds the thief's account gets nothing from it before the person notices).
func TestDashboard_NewAccountGetsNoLinkFor24Hours(t *testing.T) {
	e := telegramEnv(t)
	p := e.ch.person(1)
	e.member(1, "adult", "Levi")
	u, _ := e.db.user(p.UserID)
	e.db.mu.Lock()
	e.db.accounts[accountKey(telegram.Name, p.UserID)].LinkedAt = time.Now().Add(-time.Hour)
	e.db.mu.Unlock()

	e.says(p, "/dashboard")
	assert.Contains(t, e.lastText(), "less than 24 hours")
	assert.Empty(t, tokenIn(e.lastText()))
	assert.Empty(t, e.db.signInTokens(u.ID))
}

// Roles (Decision 1 and 5): owners, adults and the operator get a link; children and guests do
// not.
func TestDashboard_OnlyOwnersAdultsAndTheOperator(t *testing.T) {
	for role, want := range map[string]bool{"owner": true, "adult": true, "admin": true, "family": true, "child": false, "guest": false} {
		t.Run(role, func(t *testing.T) {
			e := telegramEnv(t)
			p := e.member(1, role, "Levi")
			u, _ := e.db.user(p.UserID)
			e.says(p, "/dashboard")
			assert.Equal(t, want, tokenIn(e.lastText()) != "", "role %s: %q", role, e.lastText())
			assert.Equal(t, want, len(e.db.signInTokens(u.ID)) == 1, "role %s", role)
		})
	}
}

// A newer link voids the older one; only the newest is outstanding.
func TestDashboard_ANewLinkVoidsTheOlder(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "adult", "Levi")
	u, _ := e.db.user(p.UserID)
	e.says(p, "/dashboard")
	first := tokenIn(e.lastText())
	e.says(p, "/dashboard")
	second := tokenIn(e.lastText())
	require.NotEmpty(t, first)
	require.NotEqual(t, first, second)
	toks := e.db.signInTokens(u.ID)
	require.Len(t, toks, 1)
	assert.Equal(t, identity.HashSecret(second), toks[0].TokenHash, "only the newest link is outstanding")
}

// Principle 4 and Decision 5: the token and the link are never logged, at any level.
func TestDashboard_TheLinkIsNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := telegramEnv(t)
	p := e.member(1, "adult", "Levi")
	e.says(p, "/dashboard")
	token := tokenIn(e.lastText())
	require.NotEmpty(t, token)
	e.engine.Wait()

	out := logs.String()
	assert.Contains(t, out, "sign-in link issued", "the issue is logged, by purpose and outcome")
	assert.NotContains(t, out, token)
	assert.NotContains(t, out, "/signin#")
}

// ADR 0002 Decision 1: a guest sees only the topics shared with them by name. That sharing does not
// exist yet (BL-46), so a guest gets nothing on any channel: no command, no tool, no model call.
func TestGuest_GetsNothingUntilSharingByName(t *testing.T) {
	for _, ch := range fixtures {
		t.Run(ch.name, func(t *testing.T) {
			e := newEnv(t, ch)
			g := e.member(1, "guest", "Levi")
			for _, text := range []string{"What is on the family calendar?", "/dashboard", "/accounts", "/backup"} {
				e.says(g, text)
				assert.Contains(t, e.lastText(), "not set up yet", text)
			}
			assert.Empty(t, e.model.requests(), "a guest's message never reaches the model")
			assert.Empty(t, tokenIn(strings.Join(e.texts(), "\n")))
		})
	}
}
