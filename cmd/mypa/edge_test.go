package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubHandler answers with a fixed body, so a test can see which side of the edge got a request.
func stubHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
}

// The proxy's routing, kept by the edge of the default role (V4-01). Webhooks go to the ingress,
// the Google sign-in and authenticated GraphQL go to the app, and everything else is not found:
// the playground and the health check are not public.
func TestEdgeRouting(t *testing.T) {
	edge, err := newEdgeRouter(stubHandler("ingress-response"), stubHandler("app-response"))
	require.NoError(t, err)
	srv := httptest.NewServer(edge)
	defer srv.Close()

	tests := []struct {
		name         string
		path         string
		expectedBody string
		expectedCode int
	}{
		{name: "Route Telegram to Ingress", path: "/webhook/telegram", expectedBody: "ingress-response", expectedCode: 200},
		{name: "Route Twilio to Ingress", path: "/webhook/twilio", expectedBody: "ingress-response", expectedCode: 200},
		{name: "Route Twilio Status to Ingress", path: "/webhook/twilio/status", expectedBody: "ingress-response", expectedCode: 200},
		{name: "Route Auth to App", path: "/auth/google/login", expectedBody: "app-response", expectedCode: 200},
		{name: "Route Not Found", path: "/unknown/route", expectedBody: "Not found\n", expectedCode: 404},
		{name: "Route Playground Returns 404 From Edge", path: "/playground", expectedBody: "Not found\n", expectedCode: 404},
		{name: "Route Health Returns 404 From Edge", path: "/health", expectedBody: "Not found\n", expectedCode: 404},
		{name: "Route Unauthenticated GraphQL Fails With 401", path: "/graphql", expectedBody: "Unauthorized\n", expectedCode: 401},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tt.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedCode, resp.StatusCode)

			buf := make([]byte, 1024)
			n, _ := resp.Body.Read(buf)
			assert.Equal(t, tt.expectedBody, string(buf[:n]))
		})
	}
}

// The proxy refused to start with a bad upstream address. The edge has no addresses any more, but
// it still refuses to be built with a side missing, instead of answering every request with a panic.
func TestNewEdgeRouter_MissingHandler(t *testing.T) {
	_, err := newEdgeRouter(nil, stubHandler("app"))
	assert.Error(t, err)

	_, err = newEdgeRouter(stubHandler("ingress"), nil)
	assert.Error(t, err)
}

func TestEdgeGraphQL_Authenticated(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer secret123", r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"data":{"ok":true}}`)
	})
	edge, err := newEdgeRouter(stubHandler("ingress"), app)
	require.NoError(t, err)
	srv := httptest.NewServer(edge)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/graphql", strings.NewReader(`{"query":"{__typename}"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret123")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
