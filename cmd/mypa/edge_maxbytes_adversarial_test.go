package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_Edge_MaxBytesReader verifies that oversized requests to the public edge (e.g.
// >1MB to /graphql, >10MB to /webhook/telegram) are constrained by MaxBytesReader before the
// handler behind it reads them, and do not crash the server. (The proxy test, kept for V4-01: the
// edge now hands requests to in-process handlers instead of forwarding them.)
func TestAdversarial_Edge_MaxBytesReader(t *testing.T) {
	ingress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "ingress error reading body: "+err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		fmt.Fprint(w, "ingress-ok")
	})
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "app error reading body: "+err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		fmt.Fprint(w, "app-ok")
	})

	edge, err := newEdgeRouter(ingress, app)
	require.NoError(t, err)
	srv := httptest.NewServer(edge)
	defer srv.Close()

	// Case 1: 2MB to /graphql exceeds the 1MB edge limit.
	req1, err := http.NewRequest("POST", srv.URL+"/graphql", newRepeatingReader(`{"query":"{ test }"}`, 2<<20))
	require.NoError(t, err)
	req1.Header.Set("Authorization", "Bearer x")
	resp1, err := http.DefaultClient.Do(req1)
	require.NoError(t, err)
	defer resp1.Body.Close()
	assert.True(t, resp1.StatusCode >= 400, "Oversized request to /graphql must return an error (>= 400), got %d", resp1.StatusCode)

	// Case 2: 12MB to /webhook/telegram exceeds the 10MB edge limit.
	req2, err := http.NewRequest("POST", srv.URL+"/webhook/telegram", newRepeatingReader(`{"update_id":123}`, 12<<20))
	require.NoError(t, err)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.True(t, resp2.StatusCode >= 400, "Oversized request to /webhook/telegram must return an error (>= 400), got %d", resp2.StatusCode)

	// Case 3: 2MB to the Google sign-in callback exceeds the 1MB edge limit.
	req3, err := http.NewRequest("POST", srv.URL+"/auth/google/callback", newRepeatingReader("a", 2<<20))
	require.NoError(t, err)
	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.True(t, resp3.StatusCode >= 400, "Oversized request to /auth/google must return an error (>= 400), got %d", resp3.StatusCode)

	// Case 4: a normal request afterwards still succeeds.
	respNormal, err := http.Get(srv.URL + "/auth/google/login")
	require.NoError(t, err)
	defer respNormal.Body.Close()
	assert.Equal(t, http.StatusOK, respNormal.StatusCode, "the edge must remain fully functional")
}
