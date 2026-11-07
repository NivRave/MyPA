package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/nivik/mypa/internal/markdown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestChallenge3_DoubleEscaping_TelegramPipeline simulates the full engine.sendMessage pipeline
// where markdown text is converted by markdown.ToTelegramHTML and then sent to tgClient.SendMessage.
func TestChallenge3_DoubleEscaping_TelegramPipeline(t *testing.T) {
	var capturedPayload SendMessageRequest

	mockTransport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &capturedPayload)

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
			Header:     make(http.Header),
		}, nil
	})

	client := NewClient("dummy_token")
	client.httpClient.Transport = mockTransport

	// In engine.sendMessage:
	// 1. Text is converted to Telegram HTML via markdown.ToTelegramHTML
	rawText := "Check boundaries: 5 < 10 and 20 > 15"
	formatted := markdown.ToTelegramHTML(rawText)
	require.Equal(t, "Check boundaries: 5 &lt; 10 and 20 &gt; 15", formatted)

	// 2. Chunks are generated
	chunks := markdown.SafeChunkTelegramHTML(formatted, 4000)
	require.Len(t, chunks, 1)

	// 3. tgClient.SendMessage is called with each chunk
	err := client.SendMessage(context.Background(), "chat-123", chunks[0])
	require.NoError(t, err)

	t.Logf("Raw text: %s", rawText)
	t.Logf("Formatted text: %s", formatted)
	t.Logf("Payload sent to Telegram API: %s", capturedPayload.Text)

	// Check if Telegram payload was double-escaped
	assert.NotContains(t, capturedPayload.Text, "&amp;lt;",
		"BUG-02 REGRESSION: Double escaping detected! Payload sent to Telegram was double-escaped to %s", capturedPayload.Text)
	assert.NotContains(t, capturedPayload.Text, "&amp;gt;",
		"BUG-02 REGRESSION: Double escaping detected! Payload sent to Telegram was double-escaped to %s", capturedPayload.Text)
	assert.Equal(t, "Check boundaries: 5 &lt; 10 and 20 &gt; 15", capturedPayload.Text,
		"Payload must be properly single-escaped HTML for Telegram")
}

func TestSendMessage_RawMarkdownComparisonDirect(t *testing.T) {
	var capturedPayload SendMessageRequest
	mockTransport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
			Header:     make(http.Header),
		}, nil
	})

	client := NewClient("dummy_token")
	client.httpClient.Transport = mockTransport

	// Calling client.SendMessage with raw markdown text containing comparison operators
	err := client.SendMessage(context.Background(), "chat-123", "Condition: 5 < 10 and 20 > 15")
	require.NoError(t, err)

	assert.Equal(t, "Condition: 5 &lt; 10 and 20 &gt; 15", capturedPayload.Text)
	assert.NotContains(t, capturedPayload.Text, "&amp;lt;")
	assert.NotContains(t, capturedPayload.Text, "&amp;gt;")
}

func TestSendMessageHTML_DirectDelivery(t *testing.T) {
	var capturedPayload SendMessageRequest
	mockTransport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
			Header:     make(http.Header),
		}, nil
	})

	client := NewClient("dummy_token")
	client.httpClient.Transport = mockTransport

	// Calling client.SendMessageHTML with already-formatted HTML
	err := client.SendMessageHTML(context.Background(), "chat-123", "<b>Status:</b> 5 &lt; 10")
	require.NoError(t, err)

	assert.Equal(t, "<b>Status:</b> 5 &lt; 10", capturedPayload.Text)
	assert.Equal(t, "HTML", capturedPayload.ParseMode)
}
