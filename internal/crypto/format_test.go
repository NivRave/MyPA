package crypto_test

import (
	"testing"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/crypto/cryptotest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-07: Inspect tells the two formats apart, and DecryptCurrent reads the current one only, as
// Decrypt will once V4-45 removes the unsalted fallback.
func TestInspect_TellsTheFormatsApart(t *testing.T) {
	const key = "format-test-secret"
	plaintext := []byte(`{"access_token":"abc"}`)

	current, err := crypto.Encrypt(plaintext, key)
	require.NoError(t, err)
	got, format, err := crypto.Inspect(current, key)
	require.NoError(t, err)
	assert.Equal(t, crypto.FormatCurrent, format)
	assert.Equal(t, plaintext, got)
	got, err = crypto.DecryptCurrent(current, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)

	old := cryptotest.Unsalted(t, plaintext, key)
	got, format, err = crypto.Inspect(old, key)
	require.NoError(t, err)
	assert.Equal(t, crypto.FormatUnsalted, format)
	assert.Equal(t, plaintext, got)
	_, err = crypto.DecryptCurrent(old, key)
	assert.Error(t, err, "DecryptCurrent has no fallback")
	got, err = crypto.Decrypt(old, key)
	require.NoError(t, err, "Decrypt keeps the fallback until V4-45 (DE-12)")
	assert.Equal(t, plaintext, got)

	for name, value := range map[string][]byte{
		"another key":      cryptotest.Unsalted(t, plaintext, "another-secret"),
		"not a ciphertext": plaintext,
		"empty":            nil,
	} {
		got, format, err = crypto.Inspect(value, key)
		require.NoError(t, err, name)
		assert.Equal(t, crypto.FormatUnreadable, format, name)
		assert.Nil(t, got, name)
		_, err = crypto.Decrypt(value, key)
		assert.Error(t, err, name)
	}
}
