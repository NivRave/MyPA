package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Define backend targets (configurable via env for Docker compatibility)
	gatewayStr := os.Getenv("GATEWAY_URL")
	if gatewayStr == "" {
		gatewayStr = "http://localhost:8080"
	}
	orchestratorStr := os.Getenv("ORCHESTRATOR_URL")
	if orchestratorStr == "" {
		orchestratorStr = "http://localhost:8081"
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

	// Start server on port 8000
	port := 8000
	slog.Info("unified API gateway starting", "port", port)
	
	addr := fmt.Sprintf(":%d", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("proxy server crashed", "error", err)
		os.Exit(1)
	}
}
