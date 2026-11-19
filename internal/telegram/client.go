package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client interacts with the Telegram Bot API.
type Client struct {
	token      string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client used for every Bot API call. The channel conformance
// suite (V4-34) uses it to run this client against a fake transport instead of the network.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// NewClient creates a new Telegram API client.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SendMessageRequest represents the payload for sendMessage API.
type SendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// SendMessage sends a text message to a chat. If the text is already Telegram HTML,
// it is preserved as-is; otherwise, it formats markdown to Telegram HTML.
func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.token)

	var htmlText string
	if IsTelegramHTML(text) {
		htmlText = text
	} else {
		htmlText = ToTelegramHTML(text)
	}

	reqBody := SendMessageRequest{
		ChatID:    chatID,
		Text:      htmlText,
		ParseMode: "HTML",
	}

	err := c.doSend(ctx, url, reqBody)
	if err != nil {
		// Retry without html formatting if it fails
		reqBody.ParseMode = ""
		reqBody.Text = text
		return c.doSend(ctx, url, reqBody)
	}
	return nil
}

// SendMessageHTML sends an already-formatted HTML message to a chat without conversion.
func (c *Client) SendMessageHTML(ctx context.Context, chatID, htmlText string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.token)

	reqBody := SendMessageRequest{
		ChatID:    chatID,
		Text:      htmlText,
		ParseMode: "HTML",
	}

	err := c.doSend(ctx, url, reqBody)
	if err != nil {
		// Retry without html formatting if it fails
		reqBody.ParseMode = ""
		reqBody.Text = htmlText
		return c.doSend(ctx, url, reqBody)
	}
	return nil
}

func (c *Client) doSend(ctx context.Context, url string, reqBody SendMessageRequest) error {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", redactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram API returned status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// setWebhookRequest is the payload for the setWebhook API.
type setWebhookRequest struct {
	URL         string `json:"url"`
	SecretToken string `json:"secret_token"`
}

// SetWebhook registers webhookURL with Telegram and instructs it to send secretToken in the
// X-Telegram-Bot-Api-Secret-Token header on every update. Errors never include the bot token.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secretToken string) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", c.token)

	body, err := json.Marshal(setWebhookRequest{URL: webhookURL, SecretToken: secretToken})
	if err != nil {
		return fmt.Errorf("failed to marshal setWebhook request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create setWebhook request: %w", redactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("setWebhook request failed: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("setWebhook returned status %d with unparseable body", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !result.Ok {
		return fmt.Errorf("setWebhook rejected (status %d): %s", resp.StatusCode, result.Description)
	}
	return nil
}

// redactURLError strips the request URL (which embeds the bot token) from net/http errors.
// Every client method routes transport/request errors through it (P1-1, P3-3).
func redactURLError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
	}
	return err
}

// GetFile retrieves file metadata (including file_path) from Telegram.
func (c *Client) GetFile(ctx context.Context, fileID string) (string, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", c.token, fileID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create getFile request: %w", redactURLError(err))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute getFile request: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getFile returned status: %d", resp.StatusCode)
	}

	var result struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode getFile response: %w", err)
	}

	if !result.Ok || result.Result.FilePath == "" {
		return "", fmt.Errorf("failed to get file path")
	}

	return result.Result.FilePath, nil
}

// DownloadFile downloads the raw bytes of a file from Telegram.
func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, filePath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", redactURLError(err))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download file returned status: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
