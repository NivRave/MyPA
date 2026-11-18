package telegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P3-3: net/http errors embed the request URL, which contains the bot token. No client method
// may surface it (errors end up in logs).
func TestClient_ErrorsNeverContainBotToken(t *testing.T) {
	const token = "123456:VERY-SECRET-TOKEN"
	c := NewClient(token)
	c.httpClient.Transport = failingTransport{}
	ctx := context.Background()

	checks := map[string]error{
		"SendMessage":     c.SendMessage(ctx, "chat", "hi"),
		"SendMessageHTML": c.SendMessageHTML(ctx, "chat", "<b>hi</b>"),
	}
	_, checks["GetFile"] = c.GetFile(ctx, "file-id")
	_, checks["DownloadFile"] = c.DownloadFile(ctx, "photos/p.jpg")
	checks["SetWebhook"] = c.SetWebhook(ctx, "https://x", "s")

	for name, err := range checks {
		require.Error(t, err, name)
		assert.NotContains(t, err.Error(), "VERY-SECRET-TOKEN", "%s leaked the bot token: %v", name, err)
	}
}
