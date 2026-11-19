package twilio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/doctor"
)

// Name is the channel name of the WhatsApp adapter. It is stored with audit sessions, so it never
// changes.
const Name = "whatsapp"

// maxTextLen is the longest message the adapter sends, in bytes, below WhatsApp's own 1,600
// characters. The client splits longer text with SplitMessage (ADR 0003 §3: today's value, kept).
const maxTextLen = 1500

// Webhook paths: inbound messages and delivery reports (BL-8).
const (
	WebhookPath        = channel.WebhookPrefix + "twilio"
	StatusCallbackPath = WebhookPath + "/status"
)

// API is the part of the Twilio API the adapter uses. *Client implements it; tests use a mock
// (internal/twilio/mocks).
type API interface {
	SendMessage(ctx context.Context, to, body string) error
	DownloadMedia(ctx context.Context, mediaURL string) ([]byte, error)
}

// Adapter is the WhatsApp channel adapter over Twilio (V4-31, DE-8).
type Adapter struct {
	cfg Config
	api API
}

// AdapterOption configures an Adapter.
type AdapterOption func(*Adapter)

// WithAPI replaces the Twilio client used for sending and fetching media.
func WithAPI(api API) AdapterOption {
	return func(a *Adapter) { a.api = api }
}

// NewAdapter builds the adapter from its settings. It is always registered: without an auth token
// its webhooks reject every request (fail closed), as the ingress always did.
func NewAdapter(cfg Config, opts ...AdapterOption) *Adapter {
	a := &Adapter{cfg: cfg, api: NewClient(cfg)}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name implements channel.Adapter.
func (a *Adapter) Name() string { return Name }

// Capabilities implements channel.Adapter. They are what the adapter does today. Quick-reply
// buttons (V4-10), invite links (V4-36) and voice replies (V4-18) are switched on by the items that
// build them; there are no WhatsApp groups (DE-8).
func (a *Adapter) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		AttachmentsIn: []channel.AttachmentKind{channel.KindImage, channel.KindAudio, channel.KindDocument},
		MaxTextLen:    maxTextLen,
		Markup:        channel.LightMarkup,
		Proactive:     "within 24 hours of the person's last message; outside it only an approved template (V4-33)",
	}
}

// Ingress implements channel.Adapter: Twilio calls the webhooks.
func (a *Adapter) Ingress() channel.Ingress {
	return channel.Ingress{Webhook: &channel.Webhook{Routes: []channel.Route{
		{Method: http.MethodPost, Path: WebhookPath, Handler: func(sink channel.Sink) http.Handler {
			return http.HandlerFunc(NewHandler(sinkPublisher{sink}, a.cfg.AuthToken).HandleWebhook)
		}},
		{Method: http.MethodPost, Path: StatusCallbackPath, Handler: func(sink channel.Sink) http.Handler {
			return http.HandlerFunc(NewHandler(sinkPublisher{sink}, a.cfg.AuthToken).HandleStatusCallback)
		}},
	}}}
}

// sinkPublisher lets the webhook handler publish to the adapter's sink.
type sinkPublisher struct{ sink channel.Sink }

func (p sinkPublisher) Publish(ctx context.Context, v any) error {
	in, ok := v.(channel.Inbound)
	if !ok {
		return fmt.Errorf("twilio: unexpected payload %T", v)
	}
	return p.sink(ctx, in)
}

// Send implements channel.Adapter. Messages go to the person's number; the client converts the
// text to WhatsApp markup and splits it at a paragraph, line or space, exactly as before the
// channel layer.
func (a *Adapter) Send(ctx context.Context, to channel.Address, m channel.Outbound) (channel.Receipt, error) {
	if strings.TrimSpace(to.Person) == "" {
		return channel.Receipt{}, errors.New("whatsapp: no number to send to")
	}
	if err := a.api.SendMessage(ctx, to.Person, m.Text); err != nil {
		return channel.Receipt{}, err
	}
	return channel.Receipt{Accepted: true}, nil
}

// Fetch implements channel.Adapter: the reference is the Twilio media URL, downloaded over HTTPS
// from Twilio's hosts only (see Client.DownloadMedia).
func (a *Adapter) Fetch(ctx context.Context, ref channel.AttachmentRef) (io.ReadCloser, error) {
	data, err := a.api.DownloadMedia(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Recognizes implements channel.Recognizer: a WhatsApp ID is a phone number, with or without the
// "whatsapp:" prefix. Transitional until V4-32.
func (a *Adapter) Recognizes(person string) bool {
	return strings.HasPrefix(person, "+") || strings.HasPrefix(person, "whatsapp:")
}

// HealthCheck is the adapter's /doctor check.
func (a *Adapter) HealthCheck() doctor.Check {
	return WhatsAppCheck{
		Configured:        a.cfg.AccountSID != "",
		Sender:            a.cfg.FromNumber,
		StatusCallbackURL: a.cfg.StatusCallbackURL,
	}
}

var (
	_ channel.Adapter    = (*Adapter)(nil)
	_ channel.Recognizer = (*Adapter)(nil)
)
