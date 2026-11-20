package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-35: /graphql authenticates by dashboard session only. These tests drive the middleware with
// an in-memory session store; the database path is in session_auth_test.go.

// fakeSessions is an in-memory SessionStore and identity.Store: sessions by the secret's hash, and
// each person's membership, which a test can change between requests.
type fakeSessions struct {
	*identitytest.Store
	mu       sync.Mutex
	sessions map[string]string // secret hash -> person ID
	users    map[string]models.User
	access   map[string]identity.Access
	lookups  int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{Store: identitytest.NewStore(), sessions: map[string]string{}, users: map[string]models.User{}, access: map[string]identity.Access{}}
}

// open signs a person in with role in the household labelled label and returns the secret.
func (f *fakeSessions) open(t *testing.T, label, personID string, role identity.Role) string {
	t.Helper()
	h := f.Household(label)
	secret, err := identity.NewSecret()
	require.NoError(t, err)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[identity.HashSecret(secret)] = personID
	f.users[personID] = models.User{ID: personID, PlatformID: "pk-" + personID, Role: "admin", HouseholdID: h.ID}
	f.access[personID] = identity.Access{HouseholdID: h.ID, Role: role}
	return secret
}

func (f *fakeSessions) setRole(personID string, role identity.Role) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.access[personID]
	a.Role = role
	f.access[personID] = a
}

func (f *fakeSessions) SessionPerson(_ context.Context, hash string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	p, ok := f.sessions[hash]
	if !ok {
		return "", "", identity.ErrNoSession
	}
	return "s-" + p, p, nil
}

func (f *fakeSessions) UserByID(_ context.Context, id string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

// AccessOf answers from the membership the test set, never from users.role ("admin" above).
func (f *fakeSessions) AccessOf(_ context.Context, u *models.User) (identity.Access, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.access[u.ID]
	if !ok {
		return identity.Access{}, identity.ErrNoScope
	}
	return a, nil
}

// serveAs sends one /graphql request with the Authorization header and returns the status and the
// caller the handler saw.
func serveAs(t *testing.T, f *fakeSessions, authorization string) (int, *caller) {
	t.Helper()
	var seen *caller
	var st SessionStore
	var hs identity.Store
	if f != nil {
		st, hs = f, f
	}
	h := NewAuthMiddleware(st, hs)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := callerOf(r.Context()); ok {
			seen = &c
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, seen
}

func TestAuthMiddleware_ASessionSignsThePersonIn(t *testing.T) {
	f := newFakeSessions()
	secret := f.open(t, "Levi", "p-dana", identity.RoleOwner)

	code, c := serveAs(t, f, "Bearer "+secret)
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, c)
	assert.Equal(t, "p-dana", c.scope.PersonID())
	assert.Equal(t, "pk-p-dana", c.scope.PersonKey())
	assert.Equal(t, identitytest.HouseholdID("Levi"), c.scope.HouseholdID(), "the household comes from the membership")
	assert.Equal(t, identity.RoleOwner, c.scope.Role())
	assert.Equal(t, "s-p-dana", c.sessionID)

	code, _ = serveAs(t, f, "bearer "+secret)
	assert.Equal(t, http.StatusOK, code, "the scheme is case-insensitive")
}

// ADR 0002 Decision 5: role and membership are read on every request, so a demotion or a removal
// takes effect at once.
func TestAuthMiddleware_RoleIsReadOnEveryRequest(t *testing.T) {
	f := newFakeSessions()
	secret := f.open(t, "Levi", "p-omer", identity.RoleOwner)

	_, c := serveAs(t, f, "Bearer "+secret)
	require.NotNil(t, c)
	assert.Equal(t, identity.RoleOwner, c.scope.Role())

	f.setRole("p-omer", identity.RoleChild)
	_, c = serveAs(t, f, "Bearer "+secret)
	require.NotNil(t, c)
	assert.Equal(t, identity.RoleChild, c.scope.Role(), "the demotion applies to the next request")
	r := &Resolver{}
	_, _, err := r.caller(WithCaller(context.Background(), c.scope))
	assert.ErrorContains(t, err, "owners and adults", "a child may not use the dashboard")

	f.mu.Lock()
	delete(f.access, "p-omer")
	f.mu.Unlock()
	code, c := serveAs(t, f, "Bearer "+secret)
	assert.Equal(t, http.StatusUnauthorized, code, "a person removed from the household is signed out at once")
	assert.Nil(t, c)
}

// Without a session store nothing is accepted (fail closed).
func TestAuthMiddleware_NoStoreRefusesEverything(t *testing.T) {
	secret, err := identity.NewSecret()
	require.NoError(t, err)
	code, c := serveAs(t, nil, "Bearer "+secret)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Nil(t, c)
}

// A resolver called without a signed-in caller is refused, never defaulted to a household.
func TestCaller_NoSessionIsAnError(t *testing.T) {
	r := &Resolver{}
	_, _, err := r.caller(context.Background())
	assert.Error(t, err)
	_, _, err = r.caller(WithCaller(context.Background(), identity.Scope{}))
	assert.Error(t, err, "a zero scope is no caller")
}
