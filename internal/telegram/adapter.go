package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/doctor"
)

// Name is the channel name of the Telegram adapter. It is stored with audit sessions, so it never
// changes.
const Name = "telegram"

// maxTextLen is the longest message the adapter sends, in bytes, below Telegram's own 4,096
// characters. Longer text is split by SafeChunkTelegramHTML (ADR 0003 §3: today's value, kept).
const maxTextLen = 4000

// API is the part of the Bot API the adapter uses to send and to fetch files. *Client implements
// it; tests use a mock (internal/telegram/mocks).
type API interface {
	SendMessage(ctx context.Context, chatID, text string) error
	GetFile(ctx context.Context, fileID string) (string, error)
	DownloadFile(ctx context.Context, filePath string) ([]byte, error)
}

// Adapter is the Telegram channel adapter (V4-31).
type Adapter struct {
	cfg    Config
	api    API
	client *Client // webhook registration and the /doctor webhook check
}

// AdapterOption configures an Adapter.
type AdapterOption func(*Adapter)

// WithAPI replaces the Bot API client used for sending and fetching files.
func WithAPI(api API) AdapterOption {
	return func(a *Adapter) { a.api = api }
}

// WithClient replaces the Bot API client for everything, including webhook registration. The
// channel conformance suite (V4-34) passes one with a fake transport.
func WithClient(c *Client) AdapterOption {
	return func(a *Adapter) {
		a.client = c
		a.api = c
	}
}

// NewAdapter builds the adapter from its settings. It is always registered: without a secret token
// its webhook rejects every request (fail closed), as the ingress always did.
func NewAdapter(cfg Config, opts ...AdapterOption) *Adapter {
	a := &Adapter{cfg: cfg}
	client := NewClient(cfg.BotToken)
	a.client, a.api = client, client
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name implements channel.Adapter.
func (a *Adapter) Name() string { return Name }

// Capabilities implements channel.Adapter. They are what the adapter does today. Inline buttons
// (V4-10), invite links (V4-36), group chats (V4-16), voice replies (V4-18) and documents (BL-18)
// are switched on by the items that build them.
func (a *Adapter) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		AttachmentsIn: []channel.AttachmentKind{channel.KindImage, channel.KindAudio},
		MaxTextLen:    maxTextLen,
		Markup:        channel.HTML,
		Proactive:     "always",
	}
}

// Ingress implements channel.Adapter. With a public address Telegram calls the webhook; without
// one (V4-40) the adapter polls, needs no public address and serves no route.
func (a *Adapter) Ingress() channel.Ingress {
	if a.cfg.Polling {
		return channel.Ingress{Outward: &channel.Outward{Run: func(ctx context.Context, sink channel.Sink) error {
			if !a.Configured() {
				return nil // nothing to poll for
			}
			return newPoller(a.client, sink).run(ctx)
		}}}
	}
	return channel.Ingress{Webhook: &channel.Webhook{
		Routes: []channel.Route{{
			Method:  http.MethodPost,
			Path:    WebhookPath,
			Handler: func(sink channel.Sink) http.Handler { return webhookHandler(a.cfg.SecretToken, sink) },
		}},
		Register: func(ctx context.Context) { registerWebhook(ctx, a.client, a.cfg) },
	}}
}

// Configured implements channel.Configurable: a bot token is set.
func (a *Adapter) Configured() bool { return a.cfg.BotToken != "" }

// Send implements channel.Adapter. A reply goes to the chat it came from; a message to a person
// without a known chat goes to their private chat, whose ID is their user ID. The text is
// converted to Telegram HTML and split at a paragraph, line, sentence or space with tags kept
// balanced, exactly as the engine did before the channel layer.
func (a *Adapter) Send(ctx context.Context, to channel.Address, m channel.Outbound) (channel.Receipt, error) {
	chatID := to.Conversation
	if chatID == "" {
		chatID = to.Person
	}
	if chatID == "" {
		return channel.Receipt{}, errors.New("telegram: no chat to send to")
	}
	formatted := m.Text
	if !IsTelegramHTML(formatted) {
		formatted = ToTelegramHTML(formatted)
	}
	for _, chunk := range SafeChunkTelegramHTML(formatted, maxTextLen) {
		if err := a.api.SendMessage(ctx, chatID, chunk); err != nil {
			return channel.Receipt{}, err
		}
	}
	return channel.Receipt{Accepted: true}, nil
}

// Fetch implements channel.Adapter: the reference is a Bot API file ID.
func (a *Adapter) Fetch(ctx context.Context, ref channel.AttachmentRef) (io.ReadCloser, error) {
	path, err := a.api.GetFile(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	data, err := a.api.DownloadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Recognizes implements channel.Recognizer: a Telegram user or chat ID is a decimal integer
// (negative for groups). Transitional until V4-32.
func (a *Adapter) Recognizes(person string) bool {
	digits := strings.TrimPrefix(person, "-")
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// HealthCheck is the adapter's /doctor check: Telegram's own view of the webhook, which a polling
// install expects to be absent.
func (a *Adapter) HealthCheck() doctor.Check {
	return WebhookCheck{
		Configured: a.Configured(),
		Polling:    a.cfg.Polling,
		WantURL:    a.cfg.WebhookURL,
		Info: func(ctx context.Context) (WebhookHealth, error) {
			info, err := a.client.GetWebhookInfo(ctx)
			if err != nil {
				return WebhookHealth{}, err
			}
			return WebhookHealth{
				URL:          info.URL,
				Pending:      info.PendingUpdateCount,
				LastErrorAt:  info.LastErrorAt(),
				LastErrorMsg: info.LastErrorMessage,
			}, nil
		},
	}
}

var (
	_ channel.Adapter      = (*Adapter)(nil)
	_ channel.Recognizer   = (*Adapter)(nil)
	_ channel.Configurable = (*Adapter)(nil)
)
