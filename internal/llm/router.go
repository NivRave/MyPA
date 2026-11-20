package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DefaultCooldown is how long the router skips a provider after it was unavailable, so a turn
// during an outage does not wait for the failing provider on every model call. A provider in its
// cooldown is still tried, last, when every provider is in one.
const DefaultCooldown = 60 * time.Second

// ErrNoLocalProvider is returned for a turn kept local when no local provider is configured. The
// turn is refused rather than sent to a cloud provider.
var ErrNoLocalProvider = errors.New("this turn is kept on the local model (LLM_LOCAL_DOMAINS), but no local provider is configured")

// Router sends a request to the first provider of its chain that can serve it (V4-02).
//
//   - The chain is LLM_PROVIDER followed by LLM_FALLBACK, in order. The next provider is tried
//     only when one is unavailable (*UnavailableError: quota, outage, timeout, no connection). A
//     blocked prompt or a refused request is returned as it is: another provider would not make
//     it valid, and a safety block is not something to route around.
//   - A turn kept local (WithLocalOnly) goes only to the local provider and never to the chain:
//     if the local model is down the turn fails rather than leave the machine.
type Router struct {
	chain    []Provider
	local    Provider
	cooldown time.Duration
	now      func() time.Time

	mu        sync.Mutex
	downUntil map[string]time.Time
}

// NewRouter builds a router over the chain (primary first) and an optional local provider.
func NewRouter(chain []Provider, local Provider) (*Router, error) {
	if len(chain) == 0 {
		return nil, errors.New("llm router: no provider")
	}
	if local != nil && !local.Local() {
		return nil, fmt.Errorf("llm router: %s does not run locally and cannot take local turns", local.Name())
	}
	return &Router{chain: chain, local: local, cooldown: DefaultCooldown, now: time.Now, downUntil: map[string]time.Time{}}, nil
}

// WithClock sets the router's clock (tests).
func (r *Router) WithClock(now func() time.Time) *Router {
	r.now = now
	return r
}

// Primary is the provider tried first.
func (r *Router) Primary() Provider { return r.chain[0] }

// Chain is the providers in the order they are tried.
func (r *Router) Chain() []Provider { return append([]Provider(nil), r.chain...) }

// LocalProvider is the provider local turns go to, nil when none is configured.
func (r *Router) LocalProvider() Provider { return r.local }

// order is the chain with the providers in their cooldown moved to the end, keeping the
// configured order within each group.
func (r *Router) order() []Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var up, down []Provider
	for _, p := range r.chain {
		if until, ok := r.downUntil[p.Name()]; ok && now.Before(until) {
			down = append(down, p)
			continue
		}
		up = append(up, p)
	}
	return append(up, down...)
}

func (r *Router) markDown(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downUntil[p.Name()] = r.now().Add(r.cooldown)
}

func (r *Router) markUp(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.downUntil, p.Name())
}

// Chat sends the request as described on Router.
func (r *Router) Chat(ctx context.Context, req Request) (*Response, error) {
	if IsLocalOnly(ctx) {
		if r.local == nil {
			return nil, ErrNoLocalProvider
		}
		return r.local.Chat(ctx, req)
	}
	var errs []error
	for i, p := range r.order() {
		resp, err := p.Chat(ctx, req)
		if err == nil {
			r.markUp(p)
			if i > 0 {
				slog.Warn("llm answered by a fallback provider", "provider", p.Name(), "primary", r.chain[0].Name())
			}
			return resp, nil
		}
		if !IsUnavailable(err) || ctx.Err() != nil {
			return nil, err
		}
		r.markDown(p)
		slog.Warn("llm provider unavailable", "provider", p.Name(), "error", err)
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("every llm provider is unavailable: %w", errors.Join(errs...))
}
