package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A link code is 8 symbols of Crockford's base32 (40 bits), read as people type it.
func TestLinkCode_FormAndReading(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewLinkCode()
		require.NoError(t, err)
		require.Len(t, c, LinkCodeLen)
		assert.Equal(t, c, NormalizeCode(c), "a fresh code reads as itself")
		seen[c] = true
	}
	assert.Greater(t, len(seen), 195, "codes are random")

	assert.Equal(t, "ABCD2345", NormalizeCode("abcd-2345"))
	assert.Equal(t, "ABCD2345", NormalizeCode(" ab cd 23 45 "))
	assert.Equal(t, "11010000", NormalizeCode("ILOlOooo"), "the letters left out read as the digits they look like")
	assert.Equal(t, "", NormalizeCode("ABCD234"), "too short")
	assert.Equal(t, "", NormalizeCode("ABCD23456"), "too long")
	assert.Equal(t, "", NormalizeCode("ABCD234U"), "U is not a symbol")
}

// Only a keyed hash is stored: the same code under another secret is another hash, and the hash
// does not contain the code.
func TestHashCode_IsKeyed(t *testing.T) {
	k1, k2 := []byte(strings.Repeat("a", 32)), []byte(strings.Repeat("b", 32))
	h := HashCode(k1, "abcd-2345")
	assert.Equal(t, h, HashCode(k1, "ABCD2345"), "typed forms of one code hash alike")
	assert.NotEqual(t, h, HashCode(k2, "ABCD2345"), "a different server secret gives a different hash")
	assert.NotContains(t, strings.ToUpper(h), "ABCD2345")
	assert.Len(t, h, 64)
}

// The confirmation names its own action.
func TestIsConfirmLink(t *testing.T) {
	for _, s := range []string{"CONFIRM LINK", "confirm link", "  Confirm   Link "} {
		assert.True(t, IsConfirmLink(s), s)
	}
	for _, s := range []string{"CONFIRM", "CONFIRM delete_task", "CONFIRM LINKS", "please CONFIRM LINK", "CONFIRM LINK now"} {
		assert.False(t, IsConfirmLink(s), s)
	}
}

func TestMayIssue_AfterTheQuarantine(t *testing.T) {
	now := time.Now()
	assert.False(t, MayIssue(now.Add(-NewAccountQuarantine+time.Minute), now))
	assert.True(t, MayIssue(now.Add(-NewAccountQuarantine), now))
	assert.False(t, MayIssue(time.Time{}, now), "an account without a link time is new")
}

func TestDescribe_ShowsOnlyTheEnd(t *testing.T) {
	assert.Equal(t, "whatsapp ending 0123", Describe("whatsapp", "+12025550123"))
	assert.Equal(t, "telegram ending 42", Describe("telegram", "42"))
}
