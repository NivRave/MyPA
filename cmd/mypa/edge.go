package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Body limits at the edge. The ingress router and the GraphQL handler apply their own as well;
// the edge applies them first, as the proxy did, so an oversized body is cut off before any
// handler reads it.
const (
	webhookBodyLimit = 10 << 20 // 10 MB: Telegram and Twilio webhooks
	appBodyLimit     = 1 << 20  // 1 MB: /graphql and the Google sign-in callback
)

// newEdgeRouter is the public surface of the default role, `all` (V4-01). It replaces the proxy
// that used to sit in front of the gateway and the orchestrator, with the same routes, limits and
// answers, and hands each request to the in-process ingress or app handler instead of forwarding
// it over the network:
//
//   - /webhook/telegram*, /webhook/twilio*  → ingress (10 MB)
//   - /auth/google*                         → app (1 MB)
//   - /graphql*                             → app (1 MB), only with an Authorization header
//   - /favicon.ico                          → 204, quietly (BL-6)
//   - everything else                       → 404, quietly, including /playground and /health
//
// The playground and the health check are not part of the public surface: the proxy never routed
// them, and `--role=worker` and `--role=ingress` still serve them on their own port.
func newEdgeRouter(ingress, app http.Handler) (http.Handler, error) {
	if ingress == nil {
		return nil, errors.New("edge router: no ingress handler")
	}
	if app == nil {
		return nil, errors.New("edge router: no app handler")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/webhook/telegram"), strings.HasPrefix(path, "/webhook/twilio"):
			r.Body = http.MaxBytesReader(w, r.Body, webhookBodyLimit)
			ingress.ServeHTTP(w, r)

		case strings.HasPrefix(path, "/auth/google"):
			r.Body = http.MaxBytesReader(w, r.Body, appBodyLimit)
			app.ServeHTTP(w, r)

		case strings.HasPrefix(path, "/graphql"):
			// Ingress guard, kept from the proxy: no Authorization header, no GraphQL. The auth
			// middleware behind it checks the token itself.
			if r.Header.Get("Authorization") == "" {
				slog.Warn("edge: unauthenticated request to /graphql rejected", "remote_addr", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, appBodyLimit)
			app.ServeHTTP(w, r)

		case path == "/favicon.ico":
			// BL-6: this is the public endpoint — browsers and scanners hit random paths constantly.
			// Routine, not an incident: answer quietly so real WARNs stand out.
			w.WriteHeader(http.StatusNoContent)

		default:
			slog.Debug("edge: route not found", "path", path)
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})
	return mux, nil
}
