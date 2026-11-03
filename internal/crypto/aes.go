package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

var (
	devFallbackWarningOnce sync.Once
	// Default dev/test fallback key (32 bytes)
	defaultDevFallbackKey = "mypa-default-dev-insecure-encryption-key"
)

// DeriveKey derives a 32-byte AES-256 key from a passphrase.
// If passphrase is provided and non-empty, it is hashed with SHA-256.
// If passphrase is empty, it checks the ENCRYPTION_KEY environment variable.
// If both are empty, it falls back to a deterministic development/test key with a warning.
func DeriveKey(passphrase string) []byte {
	if passphrase != "" {
		hash := sha256.Sum256([]byte(passphrase))
		return hash[:]
	}

	envKey := os.Getenv("ENCRYPTION_KEY")
	if envKey != "" {
		hash := sha256.Sum256([]byte(envKey))
		return hash[:]
	}

	devFallbackWarningOnce.Do(func() {
		slog.Warn("ENCRYPTION_KEY not set and no passphrase provided; using deterministic development/test fallback key")
	})
	hash := sha256.Sum256([]byte(defaultDevFallbackKey))
	return hash[:]
}

// Encrypt encrypts plaintext using AES-256-GCM with a key derived from passphrase.
// A cryptographically secure random 12-byte nonce is generated and prepended to the ciphertext.
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	key := DeriveKey(passphrase)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM block: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	// Seal appends the encrypted ciphertext and authentication tag to nonce
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM with a key derived from passphrase.
// It expects the first 12 bytes of ciphertext to contain the GCM nonce.
func Decrypt(ciphertext []byte, passphrase string) ([]byte, error) {
	key := DeriveKey(passphrase)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM block: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short: missing nonce")
	}

	nonce, encryptedData := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt or authenticate ciphertext: %w", err)
	}

	return plaintext, nil
}

// EncryptString encrypts a string and returns a base64-encoded ciphertext.
// If passphrase is provided, it uses it; otherwise it derives from ENCRYPTION_KEY or dev fallback.
func EncryptString(plaintext string, passphrase ...string) (string, error) {
	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	encrypted, err := Encrypt([]byte(plaintext), pw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// DecryptString decrypts a base64-encoded ciphertext and returns the plaintext string.
// If passphrase is provided, it uses it; otherwise it derives from ENCRYPTION_KEY or dev fallback.
func DecryptString(ciphertextB64 string, passphrase ...string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("invalid base64 ciphertext: %w", err)
	}

	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	plaintext, err := Decrypt(data, pw)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
