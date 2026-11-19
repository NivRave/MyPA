package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/nivik/mypa/internal/channel"
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
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"` // "private", "group", "supergroup" or "channel"
}

// Labels for the untrusted-content marks of attachments (P3-1). They appear in confirmation
// prompts and are pinned by the conformance transcripts.
const (
	photoLabel = "telegram_photo"
	voiceLabel = "telegram_voice"
)

// ParseUpdate converts a raw JSON Telegram Update into the neutral inbound message. It returns
// nil for an update that carries nothing for the assistant (no text, voice note or photo).
// Documents are not read yet (BL-18).
func ParseUpdate(body []byte) (*channel.Inbound, error) {
	var update Update
	if err := json.Unmarshal(body, &update); err != nil {
		return nil, fmt.Errorf("failed to decode telegram update: %w", err)
	}

	// We care about text messages, voice messages, or photos
	m := update.Message
	if m == nil || m.From == nil || m.Chat == nil {
		return nil, nil // Not an error, just something we ignore
	}

	text := m.Text
	if text == "" {
		text = m.Caption
	}

	var attachments []channel.AttachmentRef
	if len(m.Photo) > 0 {
		// Telegram sends an array of different sizes, the last one is the largest.
		attachments = append(attachments, channel.AttachmentRef{
			ID: m.Photo[len(m.Photo)-1].FileID, Kind: channel.KindImage, ContentType: "image/jpeg", Label: photoLabel,
		})
	}
	if m.Voice != nil {
		attachments = append(attachments, channel.AttachmentRef{
			ID: m.Voice.FileID, Kind: channel.KindAudio, ContentType: m.Voice.MimeType, Label: voiceLabel,
		})
	}
	if text == "" && len(attachments) == 0 {
		return nil, nil
	}

	conv := channel.Conversation{ID: strconv.FormatInt(m.Chat.ID, 10), Kind: channel.Direct, Addressed: true}
	switch m.Chat.Type {
	case "group", "supergroup":
		// Whether the assistant was addressed (a mention or a reply) is decided with group chats (V4-16).
		conv.Kind, conv.Addressed = channel.Group, false
	}

	return &channel.Inbound{
		Channel:      Name,
		MessageID:    strconv.Itoa(m.MessageID),
		Sender:       strconv.FormatInt(m.From.ID, 10),
		Conversation: conv,
		Text:         text,
		Attachments:  attachments,
		SentAt:       time.Unix(m.Date, 0),
	}, nil
}
