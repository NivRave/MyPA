package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"": slog.LevelInfo, "info": slog.LevelInfo, "INFO": slog.LevelInfo, "debug": slog.LevelDebug,
		" Debug ": slog.LevelDebug, "warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError,
		"verbose": slog.LevelInfo, // unknown → safe default
	}
	for in, want := range cases {
		assert.Equal(t, want, ParseLevel(in), "LOG_LEVEL=%q", in)
	}
}

// Message content is only emitted at DEBUG; INFO carries the length.
func TestMessageTextAttrs(t *testing.T) {
	var buf bytes.Buffer
	info := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	LogMessageText(context.Background(), info, "received message", "Dana's MRI results are in", "message_id", "42")
	out := buf.String()
	assert.Contains(t, out, `"text_len":25`)
	assert.Contains(t, out, `"message_id":"42"`)
	assert.NotContains(t, out, "MRI", "content must not be logged at INFO")

	buf.Reset()
	debug := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	LogMessageText(context.Background(), debug, "received message", "Dana's MRI results are in")
	assert.True(t, strings.Contains(buf.String(), "MRI"), "content is available at DEBUG")
}
