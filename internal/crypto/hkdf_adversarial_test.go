package crypto_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSEC08_HKDF_SaltedDerivation_Layout tests the exact wire format:
// [16-byte Salt][12-byte Nonce][Ciphertext + 16-byte Tag]
func TestSEC08_HKDF_SaltedDerivation_Layout(t *testing.T) {
	plaintext := []byte("secret_api_key_or_oauth_token_12345")
	passphrase := "my-strong-production-encryption-key-32"

	ciphertext, err := crypto.Encrypt(plaintext, passphrase)
	require.NoError(t, err)

	expectedLen := crypto.SaltSize + crypto.NonceSize + len(plaintext) + crypto.TagSize
	assert.Equal(t, expectedLen, len(ciphertext), "ciphertext must follow [16 salt][12 nonce][ciphertext][16 tag]")

	salt := ciphertext[:crypto.SaltSize]
	nonce := ciphertext[crypto.SaltSize : crypto.SaltSize+crypto.NonceSize]
	encryptedAndTag := ciphertext[crypto.SaltSize+crypto.NonceSize:]

	assert.Len(t, salt, 16, "salt must be 16 bytes")
	assert.Len(t, nonce, 12, "nonce must be 12 bytes")
	assert.Len(t, encryptedAndTag, len(plaintext)+16, "encrypted data must include 16-byte GCM tag")

	// Verify non-determinism across runs (random salt & nonce)
	ciphertext2, err := crypto.Encrypt(plaintext, passphrase)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(salt, ciphertext2[:crypto.SaltSize]), "salts must be randomly generated and unique per encryption")
	assert.False(t, bytes.Equal(nonce, ciphertext2[crypto.SaltSize:crypto.SaltSize+crypto.NonceSize]), "nonces must be randomly generated and unique per encryption")
	assert.False(t, bytes.Equal(ciphertext, ciphertext2), "ciphertexts must be distinct due to random salt/nonce")

	// Verify successful decryption
	decrypted, err := crypto.Decrypt(ciphertext, passphrase)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// TestSEC08_LegacyUnsaltedCiphertext_BackwardCompatibility verifies that
// legacy ciphertexts without salt are still transparently decrypted.
func TestSEC08_LegacyUnsaltedCiphertext_BackwardCompatibility(t *testing.T) {
	plaintext := []byte("legacy_plaintext_data_2025")
	passphrase := "test-legacy-passphrase"

	// Create legacy format: [12-byte nonce][ciphertext + 16-byte tag] with sha256(passphrase)
	key := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(key[:])
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)

	nonce := make([]byte, crypto.NonceSize)
	_, _ = io.ReadFull(rand.Reader, nonce)
	legacyCiphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// Decrypt using crypto.Decrypt
	decrypted, err := crypto.Decrypt(legacyCiphertext, passphrase)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted, "legacy ciphertext must be transparently decrypted")
}

// TestSEC08_ProductionStartupFails_WhenMissingKey verifies that outside unit testing the
// app (V4-01: the orchestrator is its worker) refuses to start (clean non-zero exit, no panic) if ENCRYPTION_KEY is unset —
// including when ENV=test / GO_ENV=test is set, which must no longer unlock the dev key.
func TestSEC08_ProductionStartupFails_WhenMissingKey(t *testing.T) {
	for _, envMode := range []string{"production", "test"} {
		t.Run("ENV="+envMode, func(t *testing.T) {
			cmd := exec.Command("go", "run", "./cmd/mypa")
			cmd.Dir = "../.." // project root
			cmd.Env = append(os.Environ(), "ENV="+envMode, "GO_ENV="+envMode, "ENCRYPTION_KEY=")
			output, runErr := cmd.CombinedOutput()

			require.Error(t, runErr, "the app must refuse to start when ENCRYPTION_KEY is missing")
			outStr := string(output)
			assert.Contains(t, outStr, "ENCRYPTION_KEY", "output must name the missing key")
			assert.NotContains(t, outStr, "panic:", "missing configuration must be a clean error, not a panic")
		})
	}
}
