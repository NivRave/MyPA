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
func (c *Client) SendMessage(ctx context.Context, to string, text string) error {
	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", c.cfg.AccountSID)

	formattedText := markdown.ToWhatsApp(text)

	data := url.Values{}
	if !strings.HasPrefix(to, "whatsapp:") {
		to = "whatsapp:" + to
	}
	data.Set("To", to)
	data.Set("From", c.cfg.FromNumber)
	data.Set("Body", formattedText)

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
