package twilio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/markdown"
)

// Client handles outgoing API requests to Twilio.
type Client struct {
	cfg        config.TwilioConfig
	httpClient *http.Client
}

// NewClient creates a new Twilio API client.
func NewClient(cfg config.TwilioConfig) *Client {
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{},
	}
}

// SendMessage sends a WhatsApp message (text) to the target number.
// If the formatted text exceeds the max length (1500 chars), it is split across multiple messages.
func (c *Client) SendMessage(ctx context.Context, to string, text string) error {
	formattedText := markdown.ToWhatsApp(text)
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

// DownloadMedia downloads a media file securely from Twilio using HTTP Basic Auth.
func (c *Client) DownloadMedia(ctx context.Context, mediaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("twilio API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}
