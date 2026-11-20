package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Dashboard sign-in (V4-35, ADR 0002 Decision 5, option 5b). `/dashboard` in a direct chat
// returns a link with a 256-bit token after `#`; the page submits it when its button is pressed,
// and the token becomes a session. Only a SHA-256 of the token and of the session secret is
// stored: a 256-bit random value cannot be searched for offline, so no keyed hash is needed
// (typed codes, which are short, use HashCode instead).

// SignInTTL is how long a sign-in link can be used.
const SignInTTL = 10 * time.Minute

// SessionIdle is how long a session lasts without a request; SessionMax is how long it lasts at
// most (ADR 0002 question 5).
const (
	SessionIdle = 24 * time.Hour
	SessionMax  = 7 * 24 * time.Hour
)

var (
	// ErrSignInInvalid: the sign-in token is unknown, used or expired.
	ErrSignInInvalid = errors.New("identity: the sign-in link is not valid")
	// ErrNoSession: the session is unknown, revoked, idle too long or past its lifetime.
	ErrNoSession = errors.New("identity: no valid session")
)

// secretLen is the size of a sign-in token and of a session secret: 32 bytes, 256 bits.
const secretLen = 32

// NewSecret returns a random 256-bit secret, base64url without padding (43 characters), safe in a
// URL fragment and a cookie.
func NewSecret() (string, error) {
	b := make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// WellFormedSecret reports whether s looks like a secret NewSecret made, so a malformed value is
// refused before any lookup.
func WellFormedSecret(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == secretLen
}

// HashSecret is the stored form of a sign-in token or a session secret: SHA-256, hex.
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}
