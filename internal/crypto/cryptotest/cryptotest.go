// Package cryptotest writes values in the old, unsalted encryption format for tests of the
// re-encryption pass (V4-07). Production code never writes this format; V4-45 deletes this package
// together with the fallback that reads it.
package cryptotest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"testing"
)

// Unsalted encrypts plaintext the way MyPA did before the salted format: AES-256-GCM under
// SHA-256 of the secret, written as [12-byte nonce][ciphertext + 16-byte tag].
func Unsalted(t testing.TB, plaintext []byte, secret string) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatalf("unsalted cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("unsalted GCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("unsalted nonce: %v", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil)
}
