package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/hkdf"
)

const (
	SaltSize          = 16
	NonceSize         = 12
	TagSize           = 16
	MinCiphertextSize = SaltSize + NonceSize + TagSize // 44 bytes
	hkdfInfo          = "mypa-aes-gcm"
)

var (
	devFallbackWarningOnce sync.Once
	// Default dev/test fallback key (32 bytes)
	defaultDevFallbackKey = "mypa-default-dev-insecure-encryption-key"
)

func isTestMode() bool {
	return flag.Lookup("test.v") != nil || os.Getenv("ENV") == "test" || os.Getenv("GO_ENV") == "test"
}

func getRawSecret(passphrase string) ([]byte, error) {
	if passphrase != "" {
		return []byte(passphrase), nil
	}
	envKey := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY"))
	if envKey != "" {
		return []byte(envKey), nil
	}
	if isTestMode() {
		devFallbackWarningOnce.Do(func() {
			slog.Warn("TEST MODE: ENCRYPTION_KEY not set; using deterministic dev fallback key")
		})
		return []byte(defaultDevFallbackKey), nil
	}

	slog.Error("FATAL: ENCRYPTION_KEY is required and cannot be empty in production environments")
	panic("ENCRYPTION_KEY must be configured in production")
}

// ValidateKeyConfigured ensures ENCRYPTION_KEY is available during startup.
func ValidateKeyConfigured() error {
	_, err := getRawSecret("")
	return err
}

func deriveKeyWithSalt(rawSecret, salt []byte) ([]byte, error) {
	kdf := hkdf.New(sha256.New, rawSecret, salt, []byte(hkdfInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, fmt.Errorf("hkdf key expansion failed: %w", err)
	}
	return key, nil
}

// DeriveKey derives a 32-byte AES-256 key from a passphrase or environment.
func DeriveKey(passphrase string) []byte {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		hash := sha256.Sum256([]byte(defaultDevFallbackKey))
		return hash[:]
	}
	hash := sha256.Sum256(rawSecret)
	return hash[:]
}

// Encrypt encrypts plaintext using AES-256-GCM with an HKDF-SHA256 salted key.
// Wire format: [16-byte Salt][12-byte Nonce][Ciphertext + 16-byte Tag]
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate random salt: %w", err)
	}

	key, err := deriveKeyWithSalt(rawSecret, salt)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM block: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	prefix := append(salt, nonce...)
	ciphertext := gcm.Seal(prefix, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM.
// It supports both HKDF-SHA256 salted ciphertexts and legacy unsalted ciphertexts.
func Decrypt(ciphertext []byte, passphrase string) ([]byte, error) {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		return nil, err
	}

	// Primary: HKDF salted format (>= 44 bytes)
	if len(ciphertext) >= MinCiphertextSize {
		salt := ciphertext[:SaltSize]
		nonce := ciphertext[SaltSize : SaltSize+NonceSize]
		encryptedData := ciphertext[SaltSize+NonceSize:]

		key, err := deriveKeyWithSalt(rawSecret, salt)
		if err == nil {
			block, bErr := aes.NewCipher(key)
			if bErr == nil {
				gcm, gErr := cipher.NewGCM(block)
				if gErr == nil {
					plaintext, openErr := gcm.Open(nil, nonce, encryptedData, nil)
					if openErr == nil {
						return plaintext, nil
					}
				}
			}
		}
	}

	// Fallback: Legacy unsalted format (>= 28 bytes)
	if len(ciphertext) >= NonceSize+TagSize {
		legacyKey := sha256.Sum256(rawSecret)
		block, bErr := aes.NewCipher(legacyKey[:])
		if bErr == nil {
			gcm, gErr := cipher.NewGCM(block)
			if gErr == nil {
				nonce := ciphertext[:NonceSize]
				encryptedData := ciphertext[NonceSize:]
				plaintext, openErr := gcm.Open(nil, nonce, encryptedData, nil)
				if openErr == nil {
					return plaintext, nil
				}
			}
		}
	}

	return nil, errors.New("failed to decrypt or authenticate ciphertext")
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
