package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-03: the embedding model is its own setting. Without EMBEDDING_MODEL it is the default,
// whatever the chat model is; the chat model never sets it, and it never sets the chat model.
func TestEmbeddingModel_IsSeparateFromTheChatModel(t *testing.T) {
	for _, chat := range []string{"", "some-other-chat-model"} {
		t.Setenv("GEMINI_MODEL", chat)
		t.Setenv("EMBEDDING_MODEL", "")
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, DefaultEmbeddingModel, cfg.Embedding.Model, "chat model %q", chat)
	}

	t.Setenv("GEMINI_MODEL", "some-other-chat-model")
	t.Setenv("EMBEDDING_MODEL", "another-embedding-model")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "another-embedding-model", cfg.Embedding.Model)
	assert.Equal(t, "some-other-chat-model", cfg.Gemini.Model)
}
