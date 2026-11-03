package crypto

import (
	"bytes"
	"crypto/rand"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveKey(t *testing.T) {
	// Explicit passphrase produces 32-byte key
	key1 := DeriveKey("super-secret-password")
	assert.Len(t, key1, 32)

	// Same passphrase produces identical key
	key2 := DeriveKey("super-secret-password")
	assert.Equal(t, key1, key2)

	// Different passphrase produces different key
	key3 := DeriveKey("different-password")
	assert.NotEqual(t, key1, key3)

	// Test fallback to ENCRYPTION_KEY env var
	oldEnv := os.Getenv("ENCRYPTION_KEY")
	defer func() {
		_ = os.Setenv("ENCRYPTION_KEY", oldEnv)
	}()

	_ = os.Setenv("ENCRYPTION_KEY", "env-secret-key-12345")
	envKey := DeriveKey("")
	assert.Len(t, envKey, 32)
	assert.Equal(t, DeriveKey("env-secret-key-12345"), envKey)

	// Test fallback when env var is empty
	_ = os.Unsetenv("ENCRYPTION_KEY")
	fallbackKey := DeriveKey("")
	assert.Len(t, fallbackKey, 32)
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	passphrase := "my-strong-passphrase-2026"
	testCases := []struct {
		name      string
		plaintext []byte
	}{
		{
			name:      "empty plaintext",
			plaintext: []byte(""),
		},
		{
			name:      "short text",
			plaintext: []byte("hello, world!"),
		},
		{
			name:      "json payload",
			plaintext: []byte(`{"access_token":"ya29.xyz","refresh_token":"1//abc","expires_in":3600}`),
		},
		{
			name: "large random binary payload (64KB)",
			plaintext: func() []byte {
				buf := make([]byte, 64*1024)
				_, _ = rand.Read(buf)
				return buf
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ciphertext, err := Encrypt(tc.plaintext, passphrase)
			require.NoError(t, err)
			assert.NotEmpty(t, ciphertext)

			// Ciphertext must be at least nonce size (12 bytes) + tag size (16 bytes)
			assert.GreaterOrEqual(t, len(ciphertext), 28)

			decrypted, err := Decrypt(ciphertext, passphrase)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(tc.plaintext, decrypted))
		})
	}
}

func TestEncryptDecrypt_StringHelpers(t *testing.T) {
	passphrase := "string-helper-passphrase"
	plain := "OAuth2-Refresh-Token-9876543210"

	// With explicit passphrase
	encryptedB64, err := EncryptString(plain, passphrase)
	require.NoError(t, err)
	assert.NotEmpty(t, encryptedB64)

	decrypted, err := DecryptString(encryptedB64, passphrase)
	require.NoError(t, err)
	assert.Equal(t, plain, decrypted)

	// With env fallback
	oldEnv := os.Getenv("ENCRYPTION_KEY")
	defer func() {
		_ = os.Setenv("ENCRYPTION_KEY", oldEnv)
	}()
	_ = os.Setenv("ENCRYPTION_KEY", "env-test-key")

	encryptedEnv, err := EncryptString(plain)
	require.NoError(t, err)
	decryptedEnv, err := DecryptString(encryptedEnv)
	require.NoError(t, err)
	assert.Equal(t, plain, decryptedEnv)

	// Invalid base64 in DecryptString
	_, err = DecryptString("this-is-not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecrypt_AuthenticationFailures(t *testing.T) {
	passphrase := "correct-passphrase"
	wrongPassphrase := "wrong-passphrase"
	plaintext := []byte("confidential user data")

	ciphertext, err := Encrypt(plaintext, passphrase)
	require.NoError(t, err)

	// 1. Wrong key fails
	_, err = Decrypt(ciphertext, wrongPassphrase)
	assert.Error(t, err, "decrypting with wrong passphrase must fail")

	// 2. Corrupted ciphertext body fails
	corrupted := make([]byte, len(ciphertext))
	copy(corrupted, ciphertext)
	corrupted[len(corrupted)-1] ^= 0xFF // flip last byte (auth tag)
	_, err = Decrypt(corrupted, passphrase)
	assert.Error(t, err, "decrypting tampered ciphertext must fail")

	// 3. Truncated ciphertext (< 12 bytes nonce)
	_, err = Decrypt(ciphertext[:5], passphrase)
	assert.Error(t, err, "decrypting truncated ciphertext must fail")

	// 4. Empty ciphertext
	_, err = Decrypt([]byte{}, passphrase)
	assert.Error(t, err, "decrypting empty ciphertext must fail")
}

func TestEncryptDecrypt_Concurrency(t *testing.T) {
	passphrase := "concurrent-test-key"
	const goroutines = 30
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				data := []byte("concurrent payload data for worker")
				cipher, err := Encrypt(data, passphrase)
				assert.NoError(t, err)
				plain, err := Decrypt(cipher, passphrase)
				assert.NoError(t, err)
				assert.Equal(t, data, plain)
			}
		}(i)
	}

	wg.Wait()
}
