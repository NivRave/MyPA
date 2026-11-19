package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookInfo is the part of the Bot API's getWebhookInfo answer the /doctor health check uses
// (V4-17).
type WebhookInfo struct {
	// URL is the registered webhook address; empty when no webhook is set.
	URL string `json:"url"`
	// PendingUpdateCount is the number of updates Telegram is still trying to deliver.
	PendingUpdateCount int `json:"pending_update_count"`
	// LastErrorDate is the Unix time of the most recent delivery error; 0 when there is none.
	LastErrorDate int64 `json:"last_error_date"`
	// LastErrorMessage describes that error in Telegram's words.
	LastErrorMessage string `json:"last_error_message"`
}

// LastErrorAt returns the time of the most recent delivery error, or the zero time when Telegram
// has none on record.
func (w WebhookInfo) LastErrorAt() time.Time {
	if w.LastErrorDate == 0 {
		return time.Time{}
	}
	return time.Unix(w.LastErrorDate, 0)
}

// GetWebhookInfo asks Telegram where it delivers updates for this bot and whether deliveries
// are failing. Errors never include the bot token.
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getWebhookInfo", c.token)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WebhookInfo{}, fmt.Errorf("failed to create getWebhookInfo request: %w", redactURLError(err))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return WebhookInfo{}, fmt.Errorf("getWebhookInfo request failed: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	var result struct {
		Ok          bool        `json:"ok"`
		Description string      `json:"description"`
		Result      WebhookInfo `json:"result"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := json.Unmarshal(body, &result); err != nil {
		return WebhookInfo{}, fmt.Errorf("getWebhookInfo returned status %d with unparseable body", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || !result.Ok {
		return WebhookInfo{}, fmt.Errorf("getWebhookInfo rejected (status %d): %s", resp.StatusCode, result.Description)
	}
	return result.Result, nil
}
