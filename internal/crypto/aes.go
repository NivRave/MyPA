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

// ErrKeyNotConfigured is returned when no ENCRYPTION_KEY is available outside a test binary.
var ErrKeyNotConfigured = errors.New("ENCRYPTION_KEY is not configured")

// testBinaryDetector reports whether we are running inside a `go test` binary. Only then may the
// deterministic dev key be used. Environment variables (ENV/GO_ENV) are deliberately NOT honoured:
// they can be set on a production container by accident. Overridable by tests in this package.
var testBinaryDetector = func() bool { return flag.Lookup("test.v") != nil }

func getRawSecret(passphrase string) ([]byte, error) {
	if passphrase != "" {
		return []byte(passphrase), nil
	}
	envKey := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY"))
	if envKey != "" {
		return []byte(envKey), nil
	}
	if testBinaryDetector() {
		devFallbackWarningOnce.Do(func() {
			slog.Warn("TEST BINARY: ENCRYPTION_KEY not set; using deterministic dev fallback key")
		})
		return []byte(defaultDevFallbackKey), nil
	}
	return nil, ErrKeyNotConfigured
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
// It returns nil when no key is configured (never a silent fallback key).
func DeriveKey(passphrase string) []byte {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		return nil
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

// Format is the wire format a ciphertext is in, as read with one key.
type Format int

const (
	// FormatUnreadable: neither format opens it with this key (another key, a damaged value, or
	// not a ciphertext at all).
	FormatUnreadable Format = iota
	// FormatCurrent: the salted format Encrypt writes, [salt][nonce][ciphertext+tag], its key
	// derived with HKDF-SHA256 from the secret and the value's own salt.
	FormatCurrent
	// FormatUnsalted: the old format, [nonce][ciphertext+tag] under SHA-256 of the secret. Decrypt
	// still reads it through a fallback until V4-45 removes it (BL-16, DE-12); `mypa reencrypt`
	// rewrites such values in the current format (V4-07).
	FormatUnsalted
)

// Inspect opens a ciphertext and reports which format opened it. An unreadable value is not an
// error: it returns FormatUnreadable. The error is for a missing key only.
func Inspect(ciphertext []byte, passphrase string) ([]byte, Format, error) {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		return nil, FormatUnreadable, err
	}
	if plaintext, ok := openSalted(ciphertext, rawSecret); ok {
		return plaintext, FormatCurrent, nil
	}
	if plaintext, ok := openUnsalted(ciphertext, rawSecret); ok {
		return plaintext, FormatUnsalted, nil
	}
	return nil, FormatUnreadable, nil
}

// DecryptCurrent decrypts the current (salted) format only, with no fallback: what Decrypt becomes
// once V4-45 removes the unsalted fallback.
func DecryptCurrent(ciphertext []byte, passphrase string) ([]byte, error) {
	rawSecret, err := getRawSecret(passphrase)
	if err != nil {
		return nil, err
	}
	if plaintext, ok := openSalted(ciphertext, rawSecret); ok {
		return plaintext, nil
	}
	return nil, errors.New("failed to decrypt or authenticate ciphertext")
}

// Decrypt decrypts ciphertext using AES-256-GCM.
// It supports both HKDF-SHA256 salted ciphertexts and legacy unsalted ciphertexts.
func Decrypt(ciphertext []byte, passphrase string) ([]byte, error) {
	plaintext, format, err := Inspect(ciphertext, passphrase)
	if err != nil {
		return nil, err
	}
	if format == FormatUnreadable {
		return nil, errors.New("failed to decrypt or authenticate ciphertext")
	}
	return plaintext, nil
}

// openSalted opens the current format: HKDF salted (>= 44 bytes).
func openSalted(ciphertext, rawSecret []byte) ([]byte, bool) {
	if len(ciphertext) < MinCiphertextSize {
		return nil, false
	}
	salt := ciphertext[:SaltSize]
	nonce := ciphertext[SaltSize : SaltSize+NonceSize]
	encryptedData := ciphertext[SaltSize+NonceSize:]
	key, err := deriveKeyWithSalt(rawSecret, salt)
	if err != nil {
		return nil, false
	}
	return openGCM(key, nonce, encryptedData)
}

// openUnsalted opens the legacy unsalted format (>= 28 bytes). V4-45 deletes it.
func openUnsalted(ciphertext, rawSecret []byte) ([]byte, bool) {
	if len(ciphertext) < NonceSize+TagSize {
		return nil, false
	}
	legacyKey := sha256.Sum256(rawSecret)
	return openGCM(legacyKey[:], ciphertext[:NonceSize], ciphertext[NonceSize:])
}

func openGCM(key, nonce, encryptedData []byte) ([]byte, bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, false
	}
	return plaintext, true
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
