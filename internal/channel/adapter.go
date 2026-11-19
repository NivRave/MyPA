package channel

import (
	"context"
	"io"
	"net/http"
)

// Adapter is one messaging platform behind the channel layer. Name is the only place a platform
// is named; everything else about it is behind these methods.
type Adapter interface {
	// Name is the channel's stable identifier: lower case, stored with audit sessions and used in
	// queue payloads, so it never changes once released.
	Name() string
	// Capabilities describes this configured instance (two setups of one platform may differ).
	Capabilities() Capabilities
	// Ingress says how messages arrive: through webhooks the platform calls, or a connection the
	// adapter opens itself.
	Ingress() Ingress
	// Send delivers one rendered message. An error means the platform could not be asked or did
	// not answer; a refusal it explains comes back as a Receipt that is not accepted.
	Send(ctx context.Context, to Address, m Outbound) (Receipt, error)
	// Fetch opens a file a person sent, by the reference this adapter put in Inbound.Attachments.
	Fetch(ctx context.Context, a AttachmentRef) (io.ReadCloser, error)
}

// Recognizer is implemented by an adapter that can tell whether an external person ID belongs to
// it. It exists for the callers that today hold only a person's ID (partner notifications, the
// Google sign-in confirmation) and goes away when people and their channel accounts are linked
// (V4-32).
type Recognizer interface {
	Recognizes(person string) bool
}

// Configurable is implemented by an adapter that is registered whether or not it has settings
// (an unconfigured one rejects every request). It reports whether it has them: only a configured
// adapter must be reachable (Registry.NeedPublicAddress).
type Configurable interface {
	Configured() bool
}

// Sink receives the messages an adapter's ingress produced. In the app it publishes to the durable
// inbound queue, so a webhook can answer at once and the turn follows from the queue.
type Sink func(ctx context.Context, in Inbound) error

// WebhookPrefix starts the path of every webhook route.
const WebhookPrefix = "/webhook/"

// Route is one HTTP endpoint of a webhook ingress. Handler builds the handler for the given sink;
// the handler verifies the request itself and fails closed.
type Route struct {
	Method  string
	Path    string
	Handler func(sink Sink) http.Handler
}

// Webhook is an ingress that needs a public address: the platform calls Routes.
type Webhook struct {
	Routes []Route
	// Register, when set, tells the platform where to deliver (for example by registering the
	// webhook address). It runs in the background once the ingress starts and returns when it is
	// done or ctx ends.
	Register func(ctx context.Context)
}

// Outward is an ingress that needs no public address: the adapter connects to the platform itself
// (polling or a socket) and runs until ctx ends. The first was built with V4-40.
type Outward struct {
	Run func(ctx context.Context, sink Sink) error
}

// Ingress is exactly one of Webhook or Outward.
type Ingress struct {
	Webhook *Webhook
	Outward *Outward
}

// NeedsPublicAddress reports whether the platform must be able to reach this install.
func (i Ingress) NeedsPublicAddress() bool { return i.Webhook != nil }

// Mode names the ingress kind, for /doctor and logs.
func (i Ingress) Mode() string {
	if i.Webhook != nil {
		return "webhook"
	}
	return "outward"
}
