package conformance

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	layer "github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Takeover attempts against linking (ADR 0002 Decision 4), on the suite's install: the real
// adapters, the real engine, in-memory people and accounts.

func telegramEnv(t *testing.T) *env {
	t.Helper()
	ch, ok := fixtureFor(telegram.Name)
	require.True(t, ok)
	return newEnv(t, ch)
}

// process hands the engine a message directly (for shapes the fixtures do not forge, such as a
// group chat).
func (e *env) process(msg layer.Inbound) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(e.t, e.engine.ProcessMessage(ctx, msg))
	e.engine.Wait()
}

// issueCode asks for a link code on p's account and returns it.
func (e *env) issueCode(p person) string {
	e.t.Helper()
	e.says(p, "/link")
	m := linkCodeInReply.FindStringSubmatch(e.lastText())
	require.Len(e.t, m, 2, "the reply carries the code: %q", e.lastText())
	return m[1]
}

// Codes and confirmations are shown and accepted in direct chats only: never in a group.
func TestLinking_DirectChatsOnly(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "family", "Levi")
	group := layer.Conversation{ID: "-100200", Kind: layer.Group, Addressed: true}

	e.process(layer.Inbound{Channel: telegram.Name, MessageID: "g1", Sender: p.UserID, Conversation: group, Text: "/link"})
	assert.Contains(t, e.lastText(), "direct chat")
	assert.NotRegexp(t, linkCodeInReply, strings.Join(e.texts(), "\n"), "no code is shown in a group")

	e.process(layer.Inbound{Channel: telegram.Name, MessageID: "g2", Sender: p.UserID, Conversation: group, Text: "/accounts"})
	assert.Contains(t, e.lastText(), "direct chat")

	code := e.issueCode(p)
	stranger := e.ch.person(5)
	e.process(layer.Inbound{Channel: telegram.Name, MessageID: "g3", Sender: stranger.UserID, Conversation: group, Text: "/link " + code})
	_, err := e.db.UserForAccount(context.Background(), telegram.Name, stranger.UserID)
	assert.Error(t, err)
	assert.Contains(t, e.lastText(), "direct chat", "a code typed in a group is not claimed")
	assert.Empty(t, e.model.requests(), "no step of linking reaches the model")
}

// Without LINK_CODE_SECRET, nothing is issued.
func TestLinking_RefusedWithoutTheServerSecret(t *testing.T) {
	e := telegramEnv(t)
	e.engine.WithAccounts(e.db, nil)
	p := e.member(1, "family", "Levi")
	e.says(p, "/link")
	assert.Contains(t, e.lastText(), "LINK_CODE_SECRET")
	assert.Empty(t, e.db.tokens, "no code is issued")
}

// A stolen, unlocked phone that adds the thief's account gets nothing from it for a day: a new
// account cannot issue codes.
func TestLinking_NewAccountCannotIssueCodes(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "family", "Levi")
	u, _ := e.db.user(p.UserID)
	fresh := e.ch.person(3)
	e.db.addAccount(telegram.Name, fresh.UserID, u.ID, time.Now())

	e.says(fresh, "/link")
	assert.Contains(t, e.lastText(), "less than 24 hours")
	assert.Empty(t, e.db.tokens)
}

// Guessing: every wrong code counts against the sender and against the whole install; once the
// install-wide limit is reached, even a valid code is refused until the window ends.
func TestLinking_WrongCodesAreLimitedAcrossSenders(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "family", "Levi")
	code := e.issueCode(p)

	for i := 0; i < state.GlobalCodeFailureLimit; i++ {
		guesser := fmt.Sprint(9000 + i)
		e.process(layer.Inbound{Channel: telegram.Name, MessageID: fmt.Sprint("w", i), Sender: guesser,
			Conversation: layer.Conversation{ID: guesser, Kind: layer.Direct, Addressed: true}, Text: "/link ZZZZZZZZ"})
		require.Contains(t, e.lastText(), "not valid")
	}
	late := e.ch.person(7)
	e.says(late, "/link "+code)
	assert.Contains(t, e.lastText(), "Too many wrong codes")
	assert.Nil(t, e.db.tokens[len(e.db.tokens)-1].ClaimedAt, "the valid code was not claimed")
}

// A pending approval belongs to the person and the conversation it was raised in (ADR 0002 §9):
// the person's other account cannot confirm it.
func TestApproval_ConfirmedOnlyInTheConversationItWasRaisedIn(t *testing.T) {
	e := telegramEnv(t)
	p := e.member(1, "family", "Levi")
	u, _ := e.db.user(p.UserID)
	wa, ok := fixtureFor(twilio.Name)
	require.True(t, ok)
	other := wa.person(4)
	e.db.addAccount(wa.name, other.UserID, u.ID, time.Now().Add(-48*time.Hour))

	// Untrusted content in the turn stages the deletion for confirmation.
	e.model.script(
		callTool("fetch_webpage", map[string]interface{}{"url": "https://example.com/list"}),
		callTool("delete_task", map[string]interface{}{"task_id": "77"}),
		relayLastToolResult(),
	)
	e.says(p, "Read my list and tidy it")
	require.Empty(t, e.tools.executed("delete_task"), "the gated tool waits for a confirmation")

	require.True(t, e.deliverOn(wa, inbound{From: other, Text: "CONFIRM delete_task"}))
	assert.Empty(t, e.tools.executed("delete_task"), "the other account cannot confirm it")

	e.says(p, "CONFIRM delete_task")
	assert.Len(t, e.tools.executed("delete_task"), 1, "confirmed where it was raised")
}

// memberOn adds a paired member whose account is on another channel than the scenario's.
func (e *env) memberOn(ch channel, n int, role, familyGroup string) person {
	e.t.Helper()
	p := ch.person(n)
	require.NoError(e.t, e.db.UpsertUser(models.User{
		PlatformID: p.UserID, Name: fmt.Sprintf("Member %d", n), Role: role,
		HouseholdID: e.db.household(familyGroup), FamilyGroup: familyGroup, CreatedAt: time.Now(),
	}))
	u, _ := e.db.user(p.UserID)
	e.db.addAccount(ch.name, p.UserID, u.ID, time.Now().Add(-48*time.Hour))
	return p
}

// "Type my code on your phone" (ADR 0002 Decision 4): a member gets another member of the
// household to type the member's code, to pull that person's account into theirs. The issuer's
// confirmation alone merges nothing; the other account is warned and must confirm itself.
func TestLinking_AMergeNeedsBothSides(t *testing.T) {
	e := telegramEnv(t)
	wa, ok := fixtureFor(twilio.Name)
	require.True(t, ok)
	attacker := e.member(1, "family", "Levi")
	victim := e.memberOn(wa, 2, "family", "Levi")
	ctx := context.Background()
	victimBefore, err := e.db.UserForAccount(ctx, wa.name, victim.UserID)
	require.NoError(t, err)

	code := e.issueCode(attacker)
	require.True(t, e.deliverOn(wa, inbound{From: victim, Text: "/link " + code}))
	e.says(attacker, "CONFIRM LINK")
	assert.Contains(t, e.lastText(), identity.ConfirmMergeReply, "the issuer is told the other account must confirm")
	texts := e.texts()
	assert.Contains(t, texts[len(texts)-2], "become theirs", "the other account is warned what a merge means")

	now, err := e.db.UserForAccount(ctx, wa.name, victim.UserID)
	require.NoError(t, err)
	assert.Equal(t, victimBefore.ID, now.ID, "the issuer's confirmation alone merges nothing")

	e.says(attacker, "CONFIRM MERGE")
	assert.Contains(t, e.lastText(), "Nothing is waiting", "the issuer cannot give the other side's confirmation")
	now, err = e.db.UserForAccount(ctx, wa.name, victim.UserID)
	require.NoError(t, err)
	assert.Equal(t, victimBefore.ID, now.ID)

	// The other side's own confirmation still needs the backup first: without one, nothing changes.
	require.True(t, e.deliverOn(wa, inbound{From: victim, Text: "CONFIRM MERGE"}))
	assert.Contains(t, e.lastText(), "backup failed")
	now, err = e.db.UserForAccount(ctx, wa.name, victim.UserID)
	require.NoError(t, err)
	assert.Equal(t, victimBefore.ID, now.ID, "no merge without a backup")
	assert.Empty(t, e.model.requests(), "no step reaches the model")
}

// The per-sender lockout holds for a member's account too: after five wrong codes even a valid one
// is refused.
func TestLinking_AMembersWrongCodesLockItOut(t *testing.T) {
	e := telegramEnv(t)
	guesser := e.member(1, "family", "Levi")
	owner := e.member(2, "family", "Cohen")
	for i := 0; i < 5; i++ {
		e.says(guesser, "/link ZZZZZZZZ")
	}
	assert.Contains(t, e.lastText(), "locked")
	code := e.issueCode(owner)
	e.says(guesser, "/link "+code)
	assert.Contains(t, e.lastText(), "locked out")
	assert.Nil(t, e.db.tokens[len(e.db.tokens)-1].ClaimedAt, "a locked-out account claims nothing")
}

// ADR 0002 Decision 5: a code is never logged or stored in clear, at any level, and each step of
// linking leaves an audit record (in the turn's audit session) with its outcome.
func TestLinking_CodesNeverLoggedAndEveryStepAudited(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := telegramEnv(t)
	wa, ok := fixtureFor(twilio.Name)
	require.True(t, ok)
	p := e.member(1, "family", "Levi")
	code := e.issueCode(p)
	newcomer := wa.person(3)
	require.True(t, e.deliverOn(wa, inbound{From: wa.person(4), Text: "/link ZZZZZZZZ"}))
	require.True(t, e.deliverOn(wa, inbound{From: newcomer, Text: "/link " + strings.ToLower(code)}))
	e.says(p, "CONFIRM LINK")
	_, err := e.db.UserForAccount(context.Background(), wa.name, newcomer.UserID)
	require.NoError(t, err, "the flow completed")

	hash := identity.HashCode([]byte(conformanceLinkSecret), code)
	for _, secret := range []string{code, strings.ToLower(code), hash} {
		assert.NotContains(t, logs.String(), secret, "the code or its hash appears in the logs")
	}
	assert.Contains(t, logs.String(), "[code withheld]", "the message was logged, without its code")

	e.db.mu.Lock()
	sessions := append([]models.AuditSession(nil), e.db.sessions...)
	e.db.mu.Unlock()
	var summaries []string
	for _, s := range sessions {
		for _, secret := range []string{code, strings.ToLower(code)} {
			assert.NotContains(t, s.UserPrompt, secret, "the audit session stores no code")
			assert.NotContains(t, s.FinalResponse, secret)
			assert.NotContains(t, s.ToolSummary, secret)
		}
		summaries = append(summaries, s.ToolSummary)
	}
	all := strings.Join(summaries, " | ")
	for _, outcome := range []string{"issued", "refused_invalid", "claimed", "used"} {
		assert.Contains(t, all, "account_link", "an audit record of the step")
		assert.Contains(t, all, outcome, "the audit records name the outcome %q", outcome)
	}
}
