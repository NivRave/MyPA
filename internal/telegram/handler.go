package telegram

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/models"
)

// Update represents a Telegram Update object.
type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message represents a Telegram Message object.
type Message struct {
	MessageID int    `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      *Chat  `json:"chat,omitempty"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`
}

// User represents a Telegram User object.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// Chat represents a Telegram Chat object.
type Chat struct {
	ID int64 `json:"id"`
}

// ParseUpdate converts a raw JSON Telegram Update into our internal Message model.
func ParseUpdate(body []byte) (*models.Message, error) {
	var update Update
	if err := json.Unmarshal(body, &update); err != nil {
		return nil, fmt.Errorf("failed to decode telegram update: %w", err)
	}

	// We only care about text messages for now (ignore edits, callbacks, etc.)
	if update.Message == nil || update.Message.Text == "" {
		return nil, nil // Not an error, just something we ignore
	}

	return &models.Message{
		ID:        fmt.Sprintf("%d", update.Message.MessageID),
		UserID:    fmt.Sprintf("%d", update.Message.From.ID),
		ChatID:    fmt.Sprintf("%d", update.Message.Chat.ID),
		Text:      update.Message.Text,
		Source:    "telegram",
		Timestamp: time.Unix(update.Message.Date, 0),
	}, nil
}
