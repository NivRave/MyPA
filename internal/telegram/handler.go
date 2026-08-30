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
	Text      string      `json:"text,omitempty"`
	Caption   string      `json:"caption,omitempty"`
	Voice     *Voice      `json:"voice,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
}

// PhotoSize represents a Telegram PhotoSize object.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Voice represents a Telegram Voice object.
type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type,omitempty"`
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

	// We care about text messages, voice messages, or photos
	if update.Message == nil {
		return nil, nil // Not an error, just something we ignore
	}

	text := update.Message.Text
	if text == "" {
		text = update.Message.Caption
	}

	isText := text != ""
	isVoice := update.Message.Voice != nil
	isPhoto := len(update.Message.Photo) > 0

	if !isText && !isVoice && !isPhoto {
		return nil, nil
	}

	var voiceFileID string
	if isVoice {
		voiceFileID = update.Message.Voice.FileID
	}

	var photoFileID string
	if isPhoto {
		// Telegram sends an array of different sizes, the last one is the largest.
		photoFileID = update.Message.Photo[len(update.Message.Photo)-1].FileID
	}

	return &models.Message{
		ID:          fmt.Sprintf("%d", update.Message.MessageID),
		UserID:      fmt.Sprintf("%d", update.Message.From.ID),
		ChatID:      fmt.Sprintf("%d", update.Message.Chat.ID),
		Text:        text,
		VoiceFileID: voiceFileID,
		PhotoFileID: photoFileID,
		Source:      "telegram",
		Timestamp:   time.Unix(update.Message.Date, 0),
	}, nil
}
