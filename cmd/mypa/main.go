// Command mypa is MyPA: one binary with roles (V4-01, ADR 0001 option 1c).
//
//	mypa [--role=all|ingress|worker]   run the app; all, the default, is the ingress and the worker in one process
//	mypa -migrate-only                 apply the database schema migrations and exit (P4-3)
//	mypa transfer <command>            restore and migrate (V4-43); see docs/RESTORE_AND_MIGRATE.md
//	mypa access grant owner|operator <person>  recovery: make a person an owner or the operator (V4-35)
//	mypa embeddings status|reembed     the stored memory vectors and their model (V4-03); see docs/EMBEDDINGS.md
//	mypa reencrypt [--dry-run]         rewrite old-format (unsalted) encrypted values in the current format (V4-07)
//
// Every role keeps the durable hand-off: a webhook is verified, normalised and published to the
// inbound.messages queue, and the worker consumes it from there, even inside one process. So a
// webhook returns fast, and a turn that crashes cannot lose a message that was accepted.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/deploy"
	"github.com/nivik/mypa/internal/logging"
)

func main() {
	// `mypa transfer <command>`: the restore-and-migrate tool (V4-43). A one-shot process run next
	// to a stopped app; it never starts the engine.
	if len(os.Args) > 1 && os.Args[1] == "transfer" {
		logging.Setup()
		os.Exit(transferMain(os.Args[2:]))
	}
	// `mypa access grant ...`: offline recovery of owners and the operator (V4-35).
	if len(os.Args) > 1 && os.Args[1] == "access" {
		logging.Setup()
		os.Exit(accessMain(os.Args[2:]))
	}
	// `mypa embeddings status|reembed`: the pinned embedding model and the re-embed (V4-03).
	if len(os.Args) > 1 && os.Args[1] == "embeddings" {
		logging.Setup()
		os.Exit(embeddingsMain(os.Args[2:]))
	}
	// `mypa reencrypt [--dry-run]`: the re-encryption pass (V4-07, BL-16).
	if len(os.Args) > 1 && os.Args[1] == "reencrypt" {
		logging.Setup()
		os.Exit(reencryptMain(os.Args[2:]))
	}
	// `mypa channel-setup <channel> [--submit]`: a channel's guided setup (V4-41).
	if len(os.Args) > 1 && os.Args[1] == "channel-setup" {
		logging.Setup()
		os.Exit(channelSetupMain(os.Args[2:]))
	}

	roleFlag := flag.String("role", config.RoleAll, "what this process runs: "+strings.Join(config.Roles(), ", "))
	migrateOnly := flag.Bool("migrate-only", false, "apply database schema migrations and exit (P4-3)")
	flag.Parse()

	logging.Setup() // JSON on stdout at LOG_LEVEL (default info); P3-3

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// P4-3: schema changes are an explicit step (the `migrate` compose service runs this).
	if *migrateOnly {
		os.Exit(runMigrations(cfg.Database.URL))
	}

	role, err := parseRole(*roleFlag)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(2)
	}
	os.Exit(run(cfg, role, waitForSignal))
}

// channelOptions are passed to adapters.Build by run. Empty in the app; tests put every platform
// client on a fake transport with it.
var channelOptions []adapters.Option

// checkReachable stops an install without a public address from running a configured channel
// that receives through webhooks: it could send, but nothing would ever arrive (ADR 0003 §6,
// V4-40). A channel that connects outward runs anywhere.
func checkReachable(site deploy.Settings, channels *channel.Registry) error {
	if site.Reachable() {
		return nil
	}
	need := channels.NeedPublicAddress()
	if len(need) == 0 {
		return nil
	}
	return fmt.Errorf("%s receives messages through webhooks, which need a public address, but INGRESS is none: "+
		"set INGRESS and PUBLIC_URL (see .env.example), or remove that channel's settings", strings.Join(need, " and "))
}

// role is what one process of the app runs.
type role string

const (
	roleAll     role = config.RoleAll
	roleIngress role = config.RoleIngress
	roleWorker  role = config.RoleWorker
)

func parseRole(s string) (role, error) {
	if !slices.Contains(config.Roles(), s) {
		return "", fmt.Errorf("unknown --role %q: use one of %s", s, strings.Join(config.Roles(), ", "))
	}
	return role(s), nil
}

func (r role) servesIngress() bool { return r == roleAll || r == roleIngress }
func (r role) runsWorker() bool    { return r == roleAll || r == roleWorker }

// routerForRole picks the HTTP surface of a role. Each role keeps exactly the surface of the
// service it replaces: `all` the proxy's public edge, `ingress` the gateway's router (the
// webhooks and /health), `worker` the orchestrator's (/graphql, /auth/google/callback, and the
// playground outside production). A split deploy puts its own reverse proxy in front.
func routerForRole(r role, ingress, app http.Handler) (http.Handler, error) {
	switch r {
	case roleAll:
		return newEdgeRouter(ingress, app)
	case roleIngress:
		if ingress == nil {
			return nil, errors.New("role ingress: no ingress handler")
		}
		return ingress, nil
	case roleWorker:
		if app == nil {
			return nil, errors.New("role worker: no app handler")
		}
		return app, nil
	}
	return nil, fmt.Errorf("unknown role %q", r)
}

// newHTTPServer is the app's one HTTP server, with the timeouts all three services used.
func newHTTPServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// waitForSignal returns on SIGINT or SIGTERM (nil), or with the error when the HTTP server fails.
func waitForSignal(serverErr <-chan error) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case <-quit:
		return nil
	case err := <-serverErr:
		return err
	}
}

// run starts what the role needs, serves HTTP until wait returns, then shuts down in order: stop
// accepting requests, stop the ingress, then let the worker finish and close its connections. It
// returns the process exit code.
func run(cfg *config.Config, r role, wait func(serverErr <-chan error) error) int {
	if err := cfg.Validate(string(r)); err != nil {
		slog.Error("invalid configuration — refusing to start", "role", r, "error", err)
		return 1
	}

	// Every channel adapter, built once and shared by the ingress and the worker (V4-31).
	channels, err := adapters.Build(cfg.Channels, channelOptions...)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		return 1
	}
	if err := checkReachable(cfg.Deploy, channels); err != nil {
		slog.Error("refusing to start", "error", err)
		return 1
	}

	var w *worker
	var app http.Handler
	if r.runsWorker() {
		var err error
		if w, err = startWorker(cfg, channels); err != nil {
			slog.Error("worker failed to start", "error", err)
			return 1
		}
		app = w.router
	}

	var in *ingress
	var ingressHandler http.Handler
	if r.servesIngress() {
		var err error
		if in, err = startIngress(cfg, channels); err != nil {
			slog.Error("ingress failed to start", "error", err)
			if w != nil {
				w.stop()
			}
			return 1
		}
		ingressHandler = in.router
	}

	handler, err := routerForRole(r, ingressHandler, app)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		return 1
	}

	srv := newHTTPServer(cfg.Server.Port, handler)
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("mypa http server starting", "role", r, "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			serverErr <- err
		}
	}()

	exit := 0
	if err := wait(serverErr); err != nil {
		exit = 1 // the server could not listen or failed; nothing was served from here on
	}

	slog.Info("mypa shutting down", "role", r)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shut down the http server gracefully", "error", err)
	}
	if in != nil {
		in.stop()
	}
	if w != nil {
		w.stop()
	}
	slog.Info("mypa shutdown complete", "role", r)
	return exit
}
