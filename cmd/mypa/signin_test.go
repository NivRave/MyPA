package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSignIns is an in-memory signInStore: one sign-in token, used once.
type fakeSignIns struct {
	mu        sync.Mutex
	tokenHash string
	used      bool
	user      models.User
	revoked   []string
	sessions  []models.Session
}

func (f *fakeSignIns) RedeemSignIn(_ context.Context, tokenHash, sessionHash string) (*models.AccessToken, *models.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.used || tokenHash != f.tokenHash {
		return nil, nil, identity.ErrSignInInvalid
	}
	f.used = true
	s := models.Session{ID: "sess-1", SecretHash: sessionHash, PersonID: f.user.ID, ExpiresAt: time.Now().Add(identity.SessionMax)}
	f.sessions = append(f.sessions, s)
	return &models.AccessToken{ID: "tok-1"}, &s, nil
}

func (f *fakeSignIns) RevokeSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	return nil
}

func (f *fakeSignIns) UserByID(_ context.Context, id string) (*models.User, error) {
	if id != f.user.ID {
		return nil, nil
	}
	u := f.user
	return &u, nil
}

type recordingNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (n *recordingNotifier) SendMessage(_ context.Context, userID, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, userID+": "+text)
	return nil
}

type recordingPublisher struct{ evs []events.MutationEvent }

func (p *recordingPublisher) Publish(_ context.Context, ev events.MutationEvent) error {
	p.evs = append(p.evs, ev)
	return nil
}

type signInFixture struct {
	store     *fakeSignIns
	notifier  *recordingNotifier
	publisher *recordingPublisher
	handler   http.HandlerFunc
	token     string
}

func newSignInFixture(t *testing.T, role string) signInFixture {
	t.Helper()
	st := identitytest.NewStore()
	h := st.Household("Levi")
	token, err := identity.NewSecret()
	require.NoError(t, err)
	f := signInFixture{
		store:     &fakeSignIns{tokenHash: identity.HashSecret(token), user: models.User{ID: "p-dana", PlatformID: "1001", Role: role, HouseholdID: h.ID}},
		notifier:  &recordingNotifier{},
		publisher: &recordingPublisher{},
		token:     token,
	}
	f.handler = newSignInHandler(f.store, st, f.notifier, f.publisher)
	return f
}

func postSignIn(h http.Handler, contentType, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, SignInPath, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ADR 0002 Decision 5: the token becomes a session once; the person is told in chat; the sign-in
// is recorded with the remote address in the audit record only; the token is never logged.
func TestSignIn_ExchangesTheTokenForASessionOnce(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := newSignInFixture(t, "admin")
	body := `{"token":"` + f.token + `"}`
	rr := postSignIn(f.handler, "application/json; charset=utf-8", body, "X-Forwarded-For", "192.0.2.7, 10.0.0.1")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	var resp signInResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.True(t, identity.WellFormedSecret(resp.Session))
	assert.NotEqual(t, f.token, resp.Session, "the session is a new secret, not the link's token")
	assert.Equal(t, 7*24*3600, resp.MaxAgeSeconds)
	require.Len(t, f.store.sessions, 1)
	assert.Equal(t, identity.HashSecret(resp.Session), f.store.sessions[0].SecretHash, "only the session's hash is stored")

	require.Len(t, f.notifier.sent, 1)
	assert.Contains(t, f.notifier.sent[0], "1001: ")
	assert.Contains(t, f.notifier.sent[0], "/signout", "the person is told and told how to undo it")
	require.Len(t, f.publisher.evs, 1)
	ev := f.publisher.evs[0]
	assert.Equal(t, "dashboard_session", ev.EntityType)
	assert.Equal(t, identitytest.HouseholdID("Levi"), ev.HouseholdID)
	assert.Contains(t, ev.AfterJSON, "192.0.2.7", "the remote address goes in the audit record")

	out := logs.String()
	assert.Contains(t, out, "dashboard session started")
	assert.NotContains(t, out, f.token, "the token is never logged")
	assert.NotContains(t, out, resp.Session, "nor the session")
	assert.NotContains(t, out, "192.0.2.7", "the remote address never goes to the log (principle 4)")

	again := postSignIn(f.handler, "application/json", body)
	assert.Equal(t, http.StatusUnauthorized, again.Code, "a link works once")
	assert.NotContains(t, again.Body.String(), "session")
}

// Every refusal looks the same, and nothing is asked of the store for a malformed request.
func TestSignIn_RefusesAnythingButAWellFormedJSONToken(t *testing.T) {
	f := newSignInFixture(t, "admin")
	unknown, _ := identity.NewSecret()
	cases := map[string]struct {
		contentType, body string
		code              int
	}{
		"form post (cross-site)":  {"application/x-www-form-urlencoded", "token=" + f.token, http.StatusUnsupportedMediaType},
		"text/plain (cross-site)": {"text/plain", `{"token":"` + f.token + `"}`, http.StatusUnsupportedMediaType},
		"no content type":         {"", `{"token":"` + f.token + `"}`, http.StatusUnsupportedMediaType},
		"not JSON":                {"application/json", "token", http.StatusBadRequest},
		"short token":             {"application/json", `{"token":"abc"}`, http.StatusBadRequest},
		"too large":               {"application/json", `{"token":"` + strings.Repeat("A", 8<<10) + `"}`, http.StatusBadRequest},
		"unknown token":           {"application/json", `{"token":"` + unknown + `"}`, http.StatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := postSignIn(f.handler, tc.contentType, tc.body)
			assert.Equal(t, tc.code, rr.Code)
			assert.Contains(t, rr.Body.String(), "does not work any more")
		})
	}
	req := httptest.NewRequest(http.MethodGet, SignInPath+"?token="+f.token, nil)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code, "a link preview's GET never uses a token")
	assert.False(t, f.store.used, "no refused request used the token")
	assert.Empty(t, f.notifier.sent)
}

// A person who may no longer sign in (demoted to child since the link was issued) gets no session.
func TestSignIn_APersonWhoMayNoLongerSignInGetsNoSession(t *testing.T) {
	f := newSignInFixture(t, "child")
	rr := postSignIn(f.handler, "application/json", `{"token":"`+f.token+`"}`)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Equal(t, []string{"sess-1"}, f.store.revoked, "the session made from the token is ended at once")
	assert.Empty(t, f.notifier.sent)
}

// The offline recovery command (ADR 0002 §10).
type fakeGranter struct {
	key   string
	grant db.Grant
	res   db.GrantResult
}

func (g *fakeGranter) GrantAccess(_ context.Context, key string, gr db.Grant) (db.GrantResult, error) {
	g.key, g.grant = key, gr
	return g.res, nil
}

func TestAccessGrant_Command(t *testing.T) {
	var out bytes.Buffer
	g := &fakeGranter{res: db.GrantResult{PersonID: "p-1", HouseholdID: "h-1", Changed: true}}
	assert.Equal(t, 0, runAccess(context.Background(), &out, g, []string{"grant", "owner", "1001"}))
	assert.Equal(t, "1001", g.key)
	assert.Equal(t, db.GrantOwner, g.grant)
	assert.Contains(t, out.String(), "is now an owner")

	out.Reset()
	assert.Equal(t, 0, runAccess(context.Background(), &out, g, []string{"grant", "OPERATOR", "1001"}))
	assert.Equal(t, db.GrantOperator, g.grant)

	for _, args := range [][]string{{}, {"grant"}, {"grant", "root", "1001"}, {"revoke", "owner", "1001"}} {
		out.Reset()
		assert.Equal(t, 2, runAccess(context.Background(), &out, g, args), "%v", args)
		assert.Contains(t, out.String(), "mypa access grant owner")
	}
}
