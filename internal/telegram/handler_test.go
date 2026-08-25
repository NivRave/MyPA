package telegram

import (
	"testing"
	"time"

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

	assert.Equal(t, "1", msg.ID)
	assert.Equal(t, "123456789", msg.UserID)
	assert.Equal(t, "987654321", msg.ChatID)
	assert.Equal(t, "Hello world!", msg.Text)
	assert.Equal(t, "telegram", msg.Source)
	assert.Empty(t, msg.VoiceFileID)
	assert.Equal(t, time.Unix(1620000000, 0), msg.Timestamp)
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

	assert.Equal(t, "2", msg.ID)
	assert.Equal(t, "123456789", msg.UserID)
	assert.Empty(t, msg.Text)
	assert.Equal(t, "AwADBAADbXXXXXXXXXXX", msg.VoiceFileID)
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
	assert.Nil(t, msg)
}

func TestParseUpdate_InvalidJSON(t *testing.T) {
	body := []byte(`{invalid`)
	_, err := ParseUpdate(body)
	require.Error(t, err)
}
