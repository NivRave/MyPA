// Package logging centralizes logger setup and privacy-safe logging of user content (P3-3).
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"
)

// ParseLevel maps LOG_LEVEL (debug|info|warn|warning|error, case-insensitive) to a slog level.
// Unknown or empty values default to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Setup installs a JSON logger on stdout at LOG_LEVEL as the process default and returns it.
func Setup() *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: ParseLevel(os.Getenv("LOG_LEVEL"))}))
	slog.SetDefault(logger)
	return logger
}

// LogMessageText logs an event about a user message without its content at INFO: only
// text_len (in characters) plus the caller's attributes (use message_id for correlation).
// The full text is emitted in a separate DEBUG record, so it appears only with LOG_LEVEL=debug.
//
// No hash of the text is logged: family chat is full of short, predictable messages ("ok",
// "yes"), and a hash of those is trivially reversible by dictionary.
func LogMessageText(ctx context.Context, logger *slog.Logger, msg, text string, args ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, msg, append(args, "text_len", utf8.RuneCountInString(text))...)
	if logger.Enabled(ctx, slog.LevelDebug) {
		logger.DebugContext(ctx, msg+" (content)", append(args, "text", text)...)
	}
}
