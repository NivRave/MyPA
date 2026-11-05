package main

import (
	"fmt"
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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	gatewayStr := cfg.Server.GatewayURL
	if gatewayStr == "" {
		slog.Error("GATEWAY_URL is required")
		os.Exit(1)
	}
	orchestratorStr := cfg.Server.OrchestratorURL
	if orchestratorStr == "" {
		slog.Error("ORCHESTRATOR_URL is required")
		os.Exit(1)
	}

	mux, err := setupProxy(gatewayStr, orchestratorStr)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/webhook/telegram") || strings.HasPrefix(r.URL.Path, "/webhook/twilio") {
			slog.Info("proxying to gateway", "path", r.URL.Path)
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
			gatewayProxy.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/auth/google") || strings.HasPrefix(r.URL.Path, "/graphql") || strings.HasPrefix(r.URL.Path, "/playground") {
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
