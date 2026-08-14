package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

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

	gatewayURL, _ := url.Parse(gatewayStr)
	orchestratorURL, _ := url.Parse(orchestratorStr)

	// Create reverse proxies
	gatewayProxy := httputil.NewSingleHostReverseProxy(gatewayURL)
	orchestratorProxy := httputil.NewSingleHostReverseProxy(orchestratorURL)

	// Create mux router
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/webhook/telegram") || strings.HasPrefix(r.URL.Path, "/webhook/twilio") {
			slog.Info("proxying to gateway", "path", r.URL.Path)
			gatewayProxy.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/auth/google") {
			slog.Info("proxying to orchestrator", "path", r.URL.Path)
			orchestratorProxy.ServeHTTP(w, r)
			return
		}

		slog.Warn("proxy route not found", "path", r.URL.Path)
		http.Error(w, "Not found", http.StatusNotFound)
	})

	// Start server
	port := cfg.Server.ProxyPort
	slog.Info("unified API gateway starting", "port", port)
	
	addr := fmt.Sprintf(":%d", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("proxy server crashed", "error", err)
		os.Exit(1)
	}
}
