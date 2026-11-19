package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// P3-3: the gateway must not log message text at INFO.
func TestGatewayTelegram_DoesNotLogMessageText(t *testing.T) {
	logs := captureLogs(t)
	pub := new(MockEventPublisher)
	pub.On("Publish", mock.Anything, mock.Anything).Return(nil)
	r := setupIngressRouter(pub, &config.Config{Telegram: config.TelegramConfig{SecretToken: testTelegramSecret}})

	body := `{"update_id":1,"message":{"message_id":7,"from":{"id":42},"chat":{"id":42},"text":"my bank PIN is 4321"}}`
	req := httptest.NewRequest("POST", "/webhook/telegram", bytes.NewBufferString(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", testTelegramSecret)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, logs.String(), "4321")
	assert.Contains(t, logs.String(), `"text_len"`)
}
