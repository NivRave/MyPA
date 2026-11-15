package main

import (
	"fmt"
	"github.com/nivik/mypa/internal/logging"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/config"
)

func main() {
	// Initialize logger
	logging.Setup() // JSON on stdout at LOG_LEVEL (default info); P3-3

	// Load config
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if err := cfg.Validate(config.ServiceProxy); err != nil {
		slog.Error("invalid proxy configuration — refusing to start", "error", err)
		os.Exit(1)
	}

	mux, err := setupProxy(cfg.Server.GatewayURL, cfg.Server.OrchestratorURL)
	if err != nil {
		slog.Error("failed to setup proxy", "error", err)
		os.Exit(1)
	}

	// Start server
	port := cfg.Server.ProxyPort
	slog.Info("unified API gateway starting", "port", port)

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("proxy server crashed", "error", err)
		os.Exit(1)
	}
}

func setupProxy(gatewayStr, orchestratorStr string) (*http.ServeMux, error) {
	gatewayURL, err := url.Parse(gatewayStr)
	if err != nil {
		return nil, err
	}
	orchestratorURL, err := url.Parse(orchestratorStr)
	if err != nil {
		return nil, err
	}

	gatewayProxy := httputil.NewSingleHostReverseProxy(gatewayURL)
	orchestratorProxy := httputil.NewSingleHostReverseProxy(orchestratorURL)

	proxyTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	gatewayProxy.Transport = proxyTransport
	orchestratorProxy.Transport = proxyTransport

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/webhook/telegram") || strings.HasPrefix(r.URL.Path, "/webhook/twilio") {
			slog.Info("proxying to gateway", "path", r.URL.Path)
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
			gatewayProxy.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/auth/google") {
			slog.Info("proxying to orchestrator", "path", r.URL.Path)
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
			orchestratorProxy.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/graphql") {
			// Proxy ingress guard: require Authorization header
			if r.Header.Get("Authorization") == "" {
				slog.Warn("proxy: unauthenticated request to /graphql rejected", "remote_addr", r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			slog.Info("proxying to orchestrator", "path", r.URL.Path)
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
			orchestratorProxy.ServeHTTP(w, r)
			return
		}

		slog.Warn("proxy route not found", "path", r.URL.Path)
		http.Error(w, "Not found", http.StatusNotFound)
	})

	return mux, nil
}
