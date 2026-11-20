package graph_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/packs"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingInvalidator struct {
	mu    sync.Mutex
	calls map[string][]string
}

func (r *recordingInvalidator) InvalidateFamilyPromptSections(_ context.Context, scope string, modules []string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string][]string{}
	}
	r.calls[scope] = append(r.calls[scope], modules...)
	return len(modules), nil
}

// gqlResult is one GraphQL response.
type gqlResult struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (g gqlResult) errorText() string {
	var msgs []string
	for _, e := range g.Errors {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "; ")
}

// postAs runs a query through the app's GraphQL server (graph.NewServer, with the pack guard) as
// the person signed in on ctx.
func postAs(t *testing.T, srv http.Handler, ctx context.Context, query string) gqlResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body))).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out gqlResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// V4-27: an owner switches a pack in their own household; the household lists every pack with
// its state; the pack's fields are refused while it is off and work again once it is on; Core
// fields (preferences, the personal CRM) work with every pack off; the prompt sections that list
// the household's topics and the pack's own are dropped from the cache.
func TestPacks_OwnerSwitchesAPackAndItsFieldsFollow(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	cache := &recordingInvalidator{}
	srv := graph.NewServer(graph.NewResolver(graph.ResolverConfig{DB: client, PromptCache: cache}))
	// The auth middleware builds the caller's scope from the database on every request (V4-35),
	// so each request here signs in afresh and sees the packs as they are now.
	owner := func() context.Context { return signedIn(t, client, "PackHome", "pack-owner", "admin") }
	other := func() context.Context { return signedIn(t, client, "OtherHome", "other-owner", "admin") }

	res := postAs(t, srv, owner(), `mutation { setPackEnabled(pack: "baby", enabled: false) { id packs { id enabled } } }`)
	require.Empty(t, res.Errors, res.errorText())
	var h struct {
		ID    string `json:"id"`
		Packs []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"packs"`
	}
	require.NoError(t, json.Unmarshal(res.Data["setPackEnabled"], &h))
	require.Len(t, h.Packs, len(packs.All()), "every pack is listed")
	for _, p := range h.Packs {
		assert.Equal(t, p.ID != "baby", p.Enabled, p.ID)
	}
	assert.ElementsMatch(t, []string{"schedule", "shared", "baby"}, cache.calls[h.ID], "the topic sections and the pack's own are dropped")

	res = postAs(t, srv, owner(), `{ babyProfiles { id } }`)
	assert.Contains(t, res.errorText(), "pack switched off", "a switched-off pack's query is refused")
	res = postAs(t, srv, owner(), `mutation { logBabyCare(babyId: "1", logType: "feeding") { id } }`)
	assert.Contains(t, res.errorText(), "pack switched off", "and its mutations")
	res = postAs(t, srv, other(), `{ babyProfiles { id } }`)
	assert.Empty(t, res.Errors, "another household keeps its pack: %s", res.errorText())

	res = postAs(t, srv, owner(), `mutation { setPackEnabled(pack: "baby", enabled: true) { id } }`)
	require.Empty(t, res.Errors, res.errorText())
	res = postAs(t, srv, owner(), `{ babyProfiles { id } }`)
	assert.Empty(t, res.Errors, "on again: %s", res.errorText())

	for _, p := range packs.All() {
		res = postAs(t, srv, owner(), `mutation { setPackEnabled(pack: "`+string(p.ID)+`", enabled: false) { id } }`)
		require.Empty(t, res.Errors, res.errorText())
	}
	res = postAs(t, srv, owner(), `{ personalCrmContacts { id } mutationLogs { id } household { packs { enabled } topics } }`)
	assert.Empty(t, res.Errors, "Core works with every pack off: %s", res.errorText())
	assert.NotContains(t, string(res.Data["household"]), "true")
	assert.Contains(t, string(res.Data["household"]), `"topics":["family","health","personal","work"]`, "Core topics only")
}

// Only an owner switches packs, only known packs, and never another household's.
func TestPacks_OnlyAnOwnerSwitchesKnownPacks(t *testing.T) {
	client, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	srv := graph.NewServer(graph.NewResolver(graph.ResolverConfig{DB: client}))
	adult := func() context.Context { return signedIn(t, client, "PackHome2", "pack-adult", "family") }
	owner := func() context.Context { return signedIn(t, client, "PackHome2", "pack-owner2", "admin") }

	res := postAs(t, srv, adult(), `mutation { setPackEnabled(pack: "finance", enabled: false) { id } }`)
	assert.Contains(t, res.errorText(), "permission denied")
	res = postAs(t, srv, owner(), `mutation { setPackEnabled(pack: "nonsense", enabled: true) { id } }`)
	assert.Contains(t, res.errorText(), "unknown pack")

	res = postAs(t, srv, adult(), `{ financialTransactions { id } household { packs { id enabled } } }`)
	assert.Empty(t, res.Errors, "the refused switch changed nothing: %s", res.errorText())
}
