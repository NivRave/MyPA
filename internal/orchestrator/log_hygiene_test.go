package orchestrator

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// P3-3: processing a message must not log its text at INFO.
func TestProcessMessage_DoesNotLogMessageText(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	engine, tgMock, llmMock, dbMock, cleanup := setupTestEngine(t)
	defer cleanup()
	llmMock.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return([]float32{0.1}, nil).AnyTimes()
	dbMock.EXPECT().SearchSimilarMemories(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	llmMock.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&llm.Response{Text: "noted"}, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, engine.ProcessMessage(context.Background(), channel.Inbound{
		MessageID:    "lh1",
		Sender:       "u-log",
		Conversation: channel.Conversation{ID: "c"},
		Text:         "my therapist appointment is on Tuesday",
		Channel:      "telegram",
	}))
	assert.NotContains(t, buf.String(), "therapist")
	assert.Contains(t, buf.String(), `"text_len"`)
}
