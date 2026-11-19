package twilio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client handles outgoing API requests to Twilio.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client used for every Twilio API call and media download. The
// channel conformance suite (V4-34) uses it to run this client against a fake transport instead
// of the network. The HTTPS and host allowlist checks in DownloadMedia apply regardless.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// NewClient creates a new Twilio API client.
func NewClient(cfg Config, opts ...Option) *Client {
	c := &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SendMessage sends a WhatsApp message (text) to the target number.
// If the formatted text exceeds the max length (1500 chars), it is split across multiple messages.
func (c *Client) SendMessage(ctx context.Context, to string, text string) error {
	formattedText := ToWhatsApp(text)
	chunks := SplitMessage(formattedText, 1500)

	for _, chunk := range chunks {
		if err := c.sendSingleMessage(ctx, to, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) sendSingleMessage(ctx context.Context, to string, body string) error {
	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", c.cfg.AccountSID)

	data := url.Values{}
	if !strings.HasPrefix(to, "whatsapp:") {
		to = "whatsapp:" + to
	}
	data.Set("To", to)
	data.Set("From", c.cfg.FromNumber)
	data.Set("Body", body)
	if c.cfg.StatusCallbackURL != "" {
		// BL-8: "accepted" is not "delivered"; Twilio reports the outcome to the gateway.
		data.Set("StatusCallback", c.cfg.StatusCallbackURL)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// SplitMessage splits text into chunks of at most maxLen characters,
// prioritizing cuts at double newlines (\n\n), newlines (\n), or spaces.
func SplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > maxLen {
		cut := maxLen
		if idx := strings.LastIndex(remaining[:cut], "\n\n"); idx > 0 {
			cut = idx + 2
		} else if idx := strings.LastIndex(remaining[:cut], "\n"); idx > 0 {
			cut = idx + 1
		} else if idx := strings.LastIndex(remaining[:cut], " "); idx > 0 {
			cut = idx + 1
		}

		chunk := strings.TrimSpace(remaining[:cut])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		remaining = strings.TrimSpace(remaining[cut:])
	}

	if len(remaining) > 0 {
		chunks = append(chunks, remaining)
	}

	return chunks
}

// DownloadMedia downloads a media file securely from Twilio enforcing HTTPS and hostname allowlist.
func (c *Client) DownloadMedia(ctx context.Context, mediaURL string) ([]byte, error) {
	parsedURL, err := url.Parse(mediaURL)
	if err != nil {
		return nil, fmt.Errorf("invalid media URL: %w", err)
	}

	// 1. Enforce HTTPS scheme strictly
	if !strings.EqualFold(parsedURL.Scheme, "https") {
		return nil, fmt.Errorf("insecure media URL scheme %q: HTTPS is strictly required", parsedURL.Scheme)
	}

	// 2. Validate hostname against trusted allowlist
	host := strings.ToLower(parsedURL.Hostname())
	isTrusted := host == "api.twilio.com" || host == "media.twiliocdn.com"
	if !isTrusted {
		return nil, fmt.Errorf("untrusted media URL hostname %q: only api.twilio.com and media.twiliocdn.com are permitted", host)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", parsedURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// 3. Only attach BasicAuth credentials when host matches trusted Twilio domains
	if isTrusted {
		req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("twilio API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Limit maximum downloaded media size to 25MB
	const maxMediaDownloadBytes = 25 << 20
	return io.ReadAll(io.LimitReader(resp.Body, maxMediaDownloadBytes))
}
