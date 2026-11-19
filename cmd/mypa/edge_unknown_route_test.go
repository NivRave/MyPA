package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-6: the edge is the public (ngrok) endpoint, so browsers asking for /favicon.ico and scanners
// probing random paths are routine. They must not produce WARN lines that drown real warnings.
func TestEdge_UnknownRoutesAreQuiet(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	edge, err := newEdgeRouter(http.NotFoundHandler(), http.NotFoundHandler())
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	edge.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	assert.Equal(t, http.StatusNoContent, rr.Code, "browsers get an empty favicon answer")

	rr = httptest.NewRecorder()
	edge.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/wp-login.php", nil))
	assert.Equal(t, http.StatusNotFound, rr.Code, "unknown routes are still 404")

	assert.NotContains(t, buf.String(), `"level":"WARN"`)
}
