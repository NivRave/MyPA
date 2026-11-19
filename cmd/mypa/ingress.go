package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/config"
)

// The ingress (formerly cmd/gateway, V4-01): it serves the webhook routes of every channel adapter
// (V4-31), each of which verifies and normalises its own platform's requests, and publishes each
// message to the durable inbound.messages queue. It never processes a message itself, so a webhook
// returns fast and a crash in a turn cannot lose an accepted message. It runs in the roles `all`
// and `ingress`.

// ingress is what the ingress side of the app holds while it runs.
type ingress struct {
	router   http.Handler
	pub      *broker.Publisher
	stopRegs context.CancelFunc
}

// startIngress connects the publisher (with retry, as the gateway did), builds the webhook router
// and starts, in the background, what each adapter needs to receive: registering its webhook with
// the platform, or its own outward connection. Those never stop the app.
func startIngress(cfg *config.Config, channels *channel.Registry) (*ingress, error) {
	var pub *broker.Publisher
	var err error
	for i := 0; i < 5; i++ {
		pub, err = broker.NewSafePublisher(cfg.RabbitMQ.URL, broker.InboundMessagesQueue)
		if err == nil {
			break
		}
		slog.Warn("failed to initialize rabbitmq publisher, retrying...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("rabbitmq publisher: %w", err)
	}

	regCtx, cancelReg := context.WithCancel(context.Background())
	sink := publishSink(pub)
	for _, a := range channels.All() {
		in := a.Ingress()
		slog.Info("channel ingress", "channel", a.Name(), "mode", in.Mode())
		switch {
		case in.Webhook != nil && in.Webhook.Register != nil:
			go in.Webhook.Register(regCtx)
		case in.Outward != nil:
			go func(name string, run func(context.Context, channel.Sink) error) {
				if err := run(regCtx, sink); err != nil && regCtx.Err() == nil {
					slog.Error("channel connection stopped", "channel", name, "error", err)
				}
			}(a.Name(), in.Outward.Run)
		}
	}

	return &ingress{router: setupIngressRouter(pub, channels), pub: pub, stopRegs: cancelReg}, nil
}

// stop cancels a webhook registration still in progress and closes the publisher. Call it after
// the HTTP server has stopped accepting requests.
func (in *ingress) stop() {
	in.stopRegs()
	if err := in.pub.Close(); err != nil {
		slog.Warn("closing the inbound publisher failed", "error", err)
	}
}

type EventPublisher interface {
	Publish(ctx context.Context, msg any) error
}

// publishSink hands every normalised message to the inbound queue.
func publishSink(pub EventPublisher) channel.Sink {
	return func(ctx context.Context, in channel.Inbound) error { return pub.Publish(ctx, in) }
}

func setupIngressRouter(pub EventPublisher, channels *channel.Registry) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit for gateway webhooks
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Every adapter's webhook routes, each verifying its own requests (fail closed).
	sink := publishSink(pub)
	for _, a := range channels.All() {
		if wh := a.Ingress().Webhook; wh != nil {
			for _, rt := range wh.Routes {
				r.Method(rt.Method, rt.Path, rt.Handler(sink))
			}
		}
	}
	return r
}
