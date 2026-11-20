package conformance

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake sign-in store (V4-35): memoryDB keeps sign-in tokens among its access tokens, as
// db.Client does, and counts the sessions /signout ends.

var _ orchestrator.SignInStore = (*memoryDB)(nil)

func (d *memoryDB) IssueSignIn(_ context.Context, s identity.Scope, tokenHash string) (*models.AccessToken, error) {
	if !s.Valid() || s.PersonID() == "" {
		return nil, identity.ErrNoScope
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	person := s.PersonID()
	now := time.Now()
	for _, t := range d.tokens {
		if t.Purpose == models.TokenPurposeSignin && t.PersonID != nil && *t.PersonID == person && t.UsedAt == nil {
			t.UsedAt = &now
		}
	}
	tok := &models.AccessToken{ID: uuid.NewString(), Purpose: models.TokenPurposeSignin, TokenHash: tokenHash,
		HouseholdID: s.HouseholdID(), PersonID: &person, CreatedBy: &person, ExpiresAt: now.Add(identity.SignInTTL), CreatedAt: now}
	d.tokens = append(d.tokens, tok)
	return tok, nil
}

func (d *memoryDB) RevokeSessionsOf(_ context.Context, personID string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signedOut = append(d.signedOut, personID)
	return 1, nil
}

// signInTokens returns the unused sign-in tokens stored for the person.
func (d *memoryDB) signInTokens(personID string) []models.AccessToken {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []models.AccessToken
	for _, t := range d.tokens {
		if t.Purpose == models.TokenPurposeSignin && t.PersonID != nil && *t.PersonID == personID && t.UsedAt == nil {
			out = append(out, *t)
		}
	}
	return out
}

// signInLink finds the sign-in link in a reply as the person reads it: the channel's formatting
// escapes (a backslash before punctuation, as Telegram's MarkdownV2 needs) are not part of it.
var signInLink = regexp.MustCompile(`http://localhost:3030/signin#([A-Za-z0-9_-]{43})`)

func tokenIn(text string) string {
	m := signInLink.FindStringSubmatch(strings.ReplaceAll(text, `\`, ""))
	if m == nil {
		return ""
	}
	return m[1]
}

// dashboardSignIn: on every channel, /dashboard in a direct chat gives an adult a working sign-in
// link (the token in it is the one whose hash is stored, after the channel's formatting), never
// recorded in the audit session; a child is refused and gets no link; /signout ends the person's
// sessions.
func dashboardSignIn(t *testing.T, e *env) {
	adult := e.member(1, "adult", "Levi")
	child := e.member(2, "child", "Levi")
	adultRow, _ := e.db.user(adult.UserID)
	childRow, _ := e.db.user(child.UserID)

	e.says(adult, "/dashboard")
	reply := e.lastText()
	token := tokenIn(reply)
	require.NotEmpty(t, token, "the reply carries a link with a token: %q", reply)
	assert.Contains(t, reply, "computer MyPA runs on", "a localhost link says where it opens")
	toks := e.db.signInTokens(adultRow.ID)
	require.Len(t, toks, 1)
	assert.Equal(t, identity.HashSecret(token), toks[0].TokenHash, "the link's token is the stored one, whatever the formatting")
	assert.NotContains(t, toks[0].TokenHash, token, "only the hash is stored")
	assert.Empty(t, e.model.requests(), "a command never reaches the model")

	e.engine.Wait() // the audit session is published when the turn's goroutine ends
	sessions := e.db.auditSessions()
	require.NotEmpty(t, sessions)
	last := sessions[len(sessions)-1]
	assert.NotContains(t, last.UserPrompt+last.FinalResponse+last.ToolSummary, token, "the token is never in the audit session")
	assert.Contains(t, last.ToolSummary, "dashboard_signin")

	e.says(child, "/dashboard")
	assert.Empty(t, tokenIn(e.lastText()), "a child gets no link")
	assert.Contains(t, e.lastText(), "owners and adults")
	assert.Empty(t, e.db.signInTokens(childRow.ID))

	e.says(adult, "/signout")
	assert.Contains(t, e.lastText(), "Signed out")
	assert.Equal(t, []string{adultRow.ID}, e.db.signedOutPeople())
	e.requireAllSentTo(adult, child)
	e.golden("dashboard_signin", maskedToken)
}

// maskedToken is the token after the link's "#", as the channel formats it (possibly escaped).
var maskedToken = regexp.MustCompile(`(/signin\\?#)[A-Za-z0-9_\\-]{43,}`)
