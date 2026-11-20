package identity_test

import (
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A sign-in token and a session secret are 256 random bits; only their SHA-256 is stored.
func TestNewSecret_Is256BitsAndHashedForStorage(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		s, err := identity.NewSecret()
		require.NoError(t, err)
		assert.Len(t, s, 43, "32 bytes, base64url without padding")
		assert.True(t, identity.WellFormedSecret(s))
		assert.False(t, seen[s], "secrets do not repeat")
		seen[s] = true

		h := identity.HashSecret(s)
		assert.Len(t, h, 64)
		assert.NotContains(t, h, s, "the hash does not contain the secret")
		assert.Equal(t, h, identity.HashSecret(" "+s+" "), "surrounding spaces are ignored")
	}
}

func TestWellFormedSecret_RefusesAnythingElse(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("!", 43), "123456"} {
		assert.False(t, identity.WellFormedSecret(s), "%q", s)
	}
}

// ADR 0002 Decision 5 and question 5: a link lives 10 minutes; a session 24 hours idle and 7
// days at most.
func TestSignInLifetimes(t *testing.T) {
	assert.Equal(t, "10m0s", identity.SignInTTL.String())
	assert.Equal(t, "24h0m0s", identity.SessionIdle.String())
	assert.Equal(t, "168h0m0s", identity.SessionMax.String())
}
