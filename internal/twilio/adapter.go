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

// Adapter is the WhatsApp channel adapter over Twilio (V4-31, DE-8), in the sandbox or with a
// registered sender (V4-41).
type Adapter struct {
	cfg     Config
	api     API
	content ContentAPI
}

// AdapterOption configures an Adapter.
type AdapterOption func(*Adapter)

// WithAPI replaces the Twilio client used for sending and fetching media, and for the Content API
// when it implements that too (*Client does).
func WithAPI(api API) AdapterOption {
	return func(a *Adapter) {
		a.api = api
		if c, ok := api.(ContentAPI); ok {
			a.content = c
		}
	}
}

// NewAdapter builds the adapter from its settings. It is always registered: without an auth token
// its webhooks reject every request (fail closed), as the ingress always did.
func NewAdapter(cfg Config, opts ...AdapterOption) *Adapter {
	cfg = cfg.Effective()
	client := NewClient(cfg)
	a := &Adapter{cfg: cfg, api: client, content: client}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name implements channel.Adapter.
func (a *Adapter) Name() string { return Name }

// Mode implements channel.Moded: "sandbox" or "sender" (ModeSandbox, ModeSender), from the
// settings alone (V4-41).
func (a *Adapter) Mode() string { return string(a.cfg.Mode) }

// Capabilities implements channel.Adapter. They are what the adapter does today. Quick-reply
// buttons (V4-10), invite links (V4-36) and voice replies (V4-18) are switched on by the items that
// build them; there are no WhatsApp groups (DE-8). What the two modes accept unprompted differs
// (V4-41); the rule that acts on it is V4-33.
func (a *Adapter) Capabilities() channel.Capabilities {
	proactive := "within 24 hours of the person's last message; outside it only MyPA's approved reminder and briefing templates (V4-33)"
	if a.cfg.Mode == ModeSandbox {
		proactive = "within 24 hours of the person's last message, while their sandbox membership lasts (72 hours after it); " +
			"there are no templates of MyPA's, so outside the window the message is held or sent on another channel (V4-33)"
	}
	return channel.Capabilities{
		AttachmentsIn: []channel.AttachmentKind{channel.KindImage, channel.KindAudio, channel.KindDocument},
		MaxTextLen:    maxTextLen,
		Markup:        channel.LightMarkup,
		Proactive:     proactive,
	}
}

// JoinStep implements channel.Joiner: in the sandbox, a person sends its join message to the
// sandbox number before anything they write reaches MyPA. A registered sender needs nothing.
func (a *Adapter) JoinStep() string {
	if a.cfg.Mode != ModeSandbox {
		return ""
	}
	number := strings.TrimPrefix(SandboxSender, "whatsapp:")
	if a.cfg.SandboxJoin == "" {
		return fmt.Sprintf("On WhatsApp, first send %s the sandbox's join message (the owner finds it in the Twilio Console under Messaging → Try it out); "+
			"Twilio answers that you joined.", number)
	}
	return fmt.Sprintf("On WhatsApp, first send `%s` to %s; Twilio answers that you joined. After 3 days without writing, send it again.", a.cfg.SandboxJoin, number)
}

// Ingress implements channel.Adapter: Twilio calls the webhooks, so WhatsApp always needs a public
// address (V4-40).
func (a *Adapter) Ingress() channel.Ingress {
	return channel.Ingress{Webhook: &channel.Webhook{Routes: []channel.Route{
		{Method: http.MethodPost, Path: WebhookPath, Handler: func(sink channel.Sink) http.Handler {
			return http.HandlerFunc(NewHandler(sinkPublisher{sink}, a.cfg.AuthToken).WithPublicURL(a.cfg.PublicURL).HandleWebhook)
		}},
		{Method: http.MethodPost, Path: StatusCallbackPath, Handler: func(sink channel.Sink) http.Handler {
			return http.HandlerFunc(NewHandler(sinkPublisher{sink}, a.cfg.AuthToken).WithPublicURL(a.cfg.PublicURL).HandleStatusCallback)
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

// Configured implements channel.Configurable: a Twilio account is set.
func (a *Adapter) Configured() bool { return a.cfg.AccountSID != "" }

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
// "whatsapp:" prefix.
func (a *Adapter) Recognizes(person string) bool {
	return strings.HasPrefix(person, "+") || strings.HasPrefix(person, "whatsapp:")
}

// HealthCheck is the adapter's /doctor check: the mode and its health (V4-41). activity, when
// set, says when each of a household's WhatsApp accounts last wrote; the sandbox check reads it.
func (a *Adapter) HealthCheck(activity channel.ActivitySource) doctor.Check {
	c := WhatsAppCheck{
		Configured:        a.Configured(),
		Mode:              a.cfg.Mode,
		Sender:            a.cfg.FromNumber,
		SandboxJoin:       a.cfg.SandboxJoin,
		StatusCallbackURL: a.cfg.StatusCallbackURL,
		TemplateSIDs:      a.cfg.TemplateSIDs,
	}
	if a.content != nil {
		c.Approval = a.content.TemplateApproval
	}
	if activity != nil {
		c.Activity = func(ctx context.Context, householdID string) ([]channel.AccountActivity, error) {
			return activity(ctx, householdID, Name)
		}
	}
	return c
}

var (
	_ channel.Adapter      = (*Adapter)(nil)
	_ channel.Recognizer   = (*Adapter)(nil)
	_ channel.Configurable = (*Adapter)(nil)
	_ channel.Joiner       = (*Adapter)(nil)
	_ channel.Moded        = (*Adapter)(nil)
)
