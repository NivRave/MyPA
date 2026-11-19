package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrConflict is Telegram's answer to getUpdates while another process polls the same bot or a
// webhook is set (HTTP 409).
var ErrConflict = errors.New("telegram: conflict: another getUpdates or a webhook is active for this bot")

// DeleteWebhook removes the registered webhook so the bot can be polled (V4-40). Updates waiting
// for delivery are kept. Errors never include the bot token.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	var ok bool
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, &ok)
}

// GetUpdates fetches the updates from offset on, confirming every update below it (Bot API
// getUpdates). Telegram holds the call open up to timeout while nothing is waiting. Each update is
// returned as Telegram sent it, so it is parsed exactly as a webhook body is (ParseUpdate).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]json.RawMessage, error) {
	var updates []json.RawMessage
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(timeout / time.Second)}, &updates)
	return updates, err
}

// call posts one Bot API method and decodes its result.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to marshal %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create %s request: %w", method, redactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, redactURLError(err))
	}
	defer resp.Body.Close()

	var envelope struct {
		Ok          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if resp.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s returned status %d with unparseable body", method, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !envelope.Ok {
		return fmt.Errorf("%s rejected (status %d): %s", method, resp.StatusCode, envelope.Description)
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("%s returned an unreadable result", method)
	}
	return nil
}
