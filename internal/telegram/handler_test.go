package telegram

import (
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUpdate_Text(t *testing.T) {
	body := []byte(`{
		"update_id": 10000,
		"message": {
			"message_id": 1,
			"from": {
				"id": 123456789,
				"username": "testuser"
			},
			"chat": {
				"id": 987654321
			},
			"date": 1620000000,
			"text": "Hello world!"
		}
	}`)

	msg, err := ParseUpdate(body)
	require.NoError(t, err)
	require.NotNil(t, msg)

	assert.Equal(t, "1", msg.MessageID)
	assert.Equal(t, "123456789", msg.Sender)
	assert.Equal(t, "987654321", msg.Conversation.ID)
	assert.Equal(t, channel.Direct, msg.Conversation.Kind, "a chat without a type is a private chat")
	assert.True(t, msg.Conversation.Addressed)
	assert.Equal(t, "Hello world!", msg.Text)
	assert.Equal(t, "telegram", msg.Channel)
	assert.Empty(t, msg.Attachments)
	assert.Equal(t, time.Unix(1620000000, 0), msg.SentAt)
}

func TestParseUpdate_Voice(t *testing.T) {
	body := []byte(`{
		"update_id": 10001,
		"message": {
			"message_id": 2,
			"from": {
				"id": 123456789
			},
			"chat": {
				"id": 987654321
			},
			"date": 1620000100,
			"voice": {
				"file_id": "AwADBAADbXXXXXXXXXXX",
				"duration": 5,
				"mime_type": "audio/ogg"
			}
		}
	}`)

	msg, err := ParseUpdate(body)
	require.NoError(t, err)
	require.NotNil(t, msg)

	assert.Equal(t, "2", msg.MessageID)
	assert.Equal(t, "123456789", msg.Sender)
	assert.Empty(t, msg.Text)
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, channel.AttachmentRef{ID: "AwADBAADbXXXXXXXXXXX", Kind: channel.KindAudio, ContentType: "audio/ogg", Label: "telegram_voice"}, msg.Attachments[0])
}

func TestParseUpdate_IgnoreOtherTypes(t *testing.T) {
	// e.g. a photo message without text or voice
	body := []byte(`{
		"update_id": 10002,
		"message": {
			"message_id": 3,
			"from": {"id": 1},
			"chat": {"id": 1},
			"date": 1620000200,
			"photo": [{"file_id": "123"}]
		}
	}`)

	msg, err := ParseUpdate(body)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, channel.AttachmentRef{ID: "123", Kind: channel.KindImage, ContentType: "image/jpeg", Label: "telegram_photo"}, msg.Attachments[0])
}

func TestParseUpdate_PhotoUsesTheLargestSize(t *testing.T) {
	body := []byte(`{"update_id": 1, "message": {"message_id": 4, "from": {"id": 1}, "chat": {"id": 1}, "date": 1,
		"caption": "look", "photo": [{"file_id": "small"}, {"file_id": "large"}]}}`)
	msg, err := ParseUpdate(body)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "look", msg.Text)
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, "large", msg.Attachments[0].ID)
}

func TestParseUpdate_GroupChatIsMarkedAsAGroup(t *testing.T) {
	body := []byte(`{"update_id": 1, "message": {"message_id": 5, "from": {"id": 11}, "chat": {"id": -1001, "type": "supergroup"}, "date": 1, "text": "hi all"}}`)
	msg, err := ParseUpdate(body)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, channel.Conversation{ID: "-1001", Kind: channel.Group, Addressed: false}, msg.Conversation)
	assert.Equal(t, "11", msg.Sender)
}

func TestParseUpdate_IgnoresUpdatesWithoutSenderOrChat(t *testing.T) {
	for name, body := range map[string]string{
		"no message": `{"update_id": 1, "edited_message": {"message_id": 1}}`,
		"no sender":  `{"update_id": 1, "message": {"message_id": 1, "chat": {"id": 1}, "text": "x"}}`,
		"no chat":    `{"update_id": 1, "message": {"message_id": 1, "from": {"id": 1}, "text": "x"}}`,
		"nothing":    `{"update_id": 1, "message": {"message_id": 1, "from": {"id": 1}, "chat": {"id": 1}}}`,
	} {
		msg, err := ParseUpdate([]byte(body))
		require.NoError(t, err, name)
		assert.Nil(t, msg, name)
	}
}

func TestParseUpdate_InvalidJSON(t *testing.T) {
	body := []byte(`{invalid`)
	_, err := ParseUpdate(body)
	require.Error(t, err)
}
