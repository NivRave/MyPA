package channel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ErrUnknownChannel is returned for an address or attachment on a channel that is not registered.
var ErrUnknownChannel = errors.New("channel: not registered")

// RefusedError is returned by Registry.Send when the platform did not accept the message.
type RefusedError struct {
	Channel string
	Reason  RefusalReason
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("channel %s refused the message: %s", e.Channel, e.Reason)
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Registry holds the adapters this install runs. Adapters reach the engine only by being
// registered, and registering checks their declaration.
type Registry struct {
	adapters []Adapter
	byName   map[string]Adapter
	paths    map[string]string // webhook path -> adapter name
	sealed   bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Adapter{}, paths: map[string]string{}}
}

// Register adds an adapter after checking its name, capabilities and ingress.
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return errors.New("channel: nil adapter")
	}
	name := a.Name()
	if r.sealed {
		return fmt.Errorf("channel: the registry is sealed; adapter %q must be added where the registry is built (internal/channel/adapters)", name)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("channel: invalid adapter name %q (lower case letters, digits and '-')", name)
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("channel: adapter %q is already registered", name)
	}
	if err := a.Capabilities().Validate(); err != nil {
		return fmt.Errorf("channel: adapter %q declares invalid capabilities: %w", name, err)
	}
	in := a.Ingress()
	if (in.Webhook == nil) == (in.Outward == nil) {
		return fmt.Errorf("channel: adapter %q must have exactly one ingress (webhook or outward)", name)
	}
	newPaths := map[string]bool{}
	if in.Webhook != nil {
		if len(in.Webhook.Routes) == 0 {
			return fmt.Errorf("channel: adapter %q has a webhook ingress without routes", name)
		}
		for _, rt := range in.Webhook.Routes {
			if !strings.HasPrefix(rt.Path, WebhookPrefix) || len(rt.Path) == len(WebhookPrefix) {
				return fmt.Errorf("channel: adapter %q route %q must start with %s", name, rt.Path, WebhookPrefix)
			}
			if rt.Method == "" || rt.Handler == nil {
				return fmt.Errorf("channel: adapter %q route %q needs a method and a handler", name, rt.Path)
			}
			if owner, taken := r.paths[rt.Path]; taken || newPaths[rt.Path] {
				return fmt.Errorf("channel: adapter %q route %q is already served by %q", name, rt.Path, owner)
			}
			newPaths[rt.Path] = true
		}
	} else if in.Outward.Run == nil {
		return fmt.Errorf("channel: adapter %q has an outward ingress without Run", name)
	}

	for p := range newPaths {
		r.paths[p] = name
	}
	r.adapters = append(r.adapters, a)
	r.byName[name] = a
	return nil
}

// Seal makes the registry refuse every later Register. The app's registry is sealed where it is
// built (adapters.Build), so the adapters the engine can reach are exactly the ones the channel
// conformance suite runs (V4-34).
func (r *Registry) Seal() { r.sealed = true }

// Sealed reports whether Seal was called.
func (r *Registry) Sealed() bool { return r != nil && r.sealed }

// Get returns the adapter with the given name.
func (r *Registry) Get(name string) (Adapter, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.byName[name]
	return a, ok
}

// All returns the adapters in the order they were registered.
func (r *Registry) All() []Adapter {
	if r == nil {
		return nil
	}
	return append([]Adapter(nil), r.adapters...)
}

// Recognize returns an address for a person known only by an external ID, asking each adapter
// that implements Recognizer in registration order. Transitional until V4-32 (see Recognizer).
func (r *Registry) Recognize(person string) (Address, bool) {
	if r == nil || strings.TrimSpace(person) == "" {
		return Address{}, false
	}
	for _, a := range r.adapters {
		if rec, ok := a.(Recognizer); ok && rec.Recognizes(person) {
			return Address{Channel: a.Name(), Person: person}, true
		}
	}
	return Address{}, false
}

// Send renders m for the destination adapter's capabilities and sends it. A refusal is returned
// as a *RefusedError, so callers that only need "did it go out" can treat every error alike.
func (r *Registry) Send(ctx context.Context, to Address, m Outbound) (Receipt, error) {
	a, ok := r.Get(to.Channel)
	if !ok {
		return Receipt{}, fmt.Errorf("%w: %q", ErrUnknownChannel, to.Channel)
	}
	if err := m.Validate(); err != nil {
		return Receipt{}, err
	}
	rcpt, err := a.Send(ctx, to, Render(a.Capabilities(), m))
	if err != nil {
		return rcpt, err
	}
	if !rcpt.Accepted {
		return rcpt, &RefusedError{Channel: to.Channel, Reason: rcpt.Reason}
	}
	return rcpt, nil
}

// SendText sends plain assistant text.
func (r *Registry) SendText(ctx context.Context, to Address, text string) error {
	_, err := r.Send(ctx, to, Outbound{Text: text})
	return err
}

// MaxFetchBytes bounds how much of one attachment FetchAll reads.
const MaxFetchBytes = 25 << 20

// FetchAll reads an attachment of a message received on the named channel, up to MaxFetchBytes.
func (r *Registry) FetchAll(ctx context.Context, channelName string, ref AttachmentRef) ([]byte, error) {
	a, ok := r.Get(channelName)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownChannel, channelName)
	}
	rc, err := a.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, MaxFetchBytes)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// NeedPublicAddress returns, in registration order, the configured adapters whose ingress is a
// webhook. An install without a public address cannot run them: the app refuses to start rather
// than run a channel that can send but never receives (ADR 0003 §6, V4-40). An adapter that does
// not implement Configurable counts as configured.
func (r *Registry) NeedPublicAddress() []string {
	var names []string
	for _, a := range r.All() {
		if c, ok := a.(Configurable); ok && !c.Configured() {
			continue
		}
		if a.Ingress().NeedsPublicAddress() {
			names = append(names, a.Name())
		}
	}
	return names
}

// WebhookPaths returns every webhook path served by the registered adapters.
func (r *Registry) WebhookPaths() []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, a := range r.adapters {
		if wh := a.Ingress().Webhook; wh != nil {
			for _, rt := range wh.Routes {
				out = append(out, rt.Path)
			}
		}
	}
	return out
}

// Validate reports an Outbound message no adapter should be asked to send.
func (m Outbound) Validate() error {
	for i, c := range m.Choices {
		if c.ID == "" || len(c.ID) > MaxChoiceIDLen {
			return fmt.Errorf("channel: choice %d has an ID of %d bytes (1 to %d allowed)", i+1, len(c.ID), MaxChoiceIDLen)
		}
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Errorf("channel: choice %d has no label", i+1)
		}
	}
	return nil
}
