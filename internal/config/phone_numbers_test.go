package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// phoneLike matches an E.164-looking number. The leading boundary keeps base64 runs (go.sum,
// package-lock integrity hashes) out: there a "+" follows a letter or digit.
var phoneLike = regexp.MustCompile(`(?:^|[^A-Za-z0-9/+=])(\+[1-9][0-9]{7,14})\b`)

// dialableNANP is the shape of a real North American number: 10 national digits, and neither the
// area code nor the exchange starts with 0 or 1. Test dummies such as +1234567890 (too short) or
// +15550001111 (exchange 000) cannot reach anyone and are fine.
var dialableNANP = regexp.MustCompile(`^\+1[2-9][0-9]{2}[2-9][0-9]{6}$`)

// fictionalNANP is the range the North American Numbering Plan reserves for fiction:
// any area code, exchange 555, line 0100–0199 (e.g. +1 202 555 0123).
var fictionalNANP = regexp.MustCompile(`^\+1[2-9][0-9]{2}55501[0-9]{2}$`)

// knownNonPersonal are dialable committed numbers that belong to no person, each with its reason.
var knownNonPersonal = map[string]string{
	"+14155238886": "Twilio's published WhatsApp sandbox sender",
}

// couldBePersonal reports whether p might be someone's real phone. Outside NANP the test cannot
// tell a dummy from a real number, so every other country code counts as real.
func couldBePersonal(p string) bool {
	if knownNonPersonal[p] != "" {
		return false
	}
	if strings.HasPrefix(p, "+1") {
		return dialableNANP.MatchString(p) && !fictionalNANP.MatchString(p)
	}
	return true
}

// maskPhone keeps a failure message from republishing the number it is complaining about.
func maskPhone(p string) string {
	if len(p) <= 4 {
		return p
	}
	return p[:4] + strings.Repeat("•", len(p)-4)
}

// BL-13: no committed file may carry a real person's phone number — a family member's number in
// an example, a doc or a test fixture is published with the repository and stays in its history.
// Scans what git tracks (never the working tree, which holds the real .env).
func TestNoRealPhoneNumbersCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err)

	files, found := 0, 0
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			continue // deleted in the working tree, or binary
		}
		files++
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range phoneLike.FindAllStringSubmatch(line, -1) {
				p := m[1]
				found++
				if !couldBePersonal(p) {
					continue
				}
				assert.Failf(t, "real-format phone number committed",
					"%s:%d: %s — use the fictional range +1 NXX 555 01XX (e.g. +12025550123)", name, i+1, maskPhone(p))
			}
		}
	}
	assert.Greater(t, files, 100, "expected to scan the tracked tree")
	assert.Greater(t, found, 10, "expected to see the known test numbers; is phoneLike still matching?")
}

func TestCouldBePersonal(t *testing.T) {
	// Dialable, non-fictional examples are split so this file does not trip the guard above.
	for _, p := range []string{"+1202555" + "0200", "+1202555" + "1234", "+4420" + "79460000", "+9725" + "00000000"} {
		assert.True(t, couldBePersonal(p), "%s could reach a person", maskPhone(p))
	}
	for _, p := range []string{
		"+12025550123", "+13105550199", // reserved for fiction
		"+14155238886",                 // Twilio sandbox
		"+1234567890", "+15550001", "+15550001111", "+15550100123", // not dialable in NANP
	} {
		assert.False(t, couldBePersonal(p), p)
	}
	assert.Equal(t, "+972••••", maskPhone("+9725678"))
	assert.Equal(t, "+12025550123", phoneLike.FindStringSubmatch(`"+12025550123"`)[1])
	assert.Nil(t, phoneLike.FindStringSubmatch("h1:abc+12025550123"), "base64 run, not a number")
}
