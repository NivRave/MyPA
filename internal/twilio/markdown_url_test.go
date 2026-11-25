package twilio

import (
	"encoding/base64"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A bare URL reaches the person exactly as written: "__abc__" in a sign-in token is not turned
// into WhatsApp bold ("*abc*"), which broke the link (CI run 45569046251, bareurl).
func TestToWhatsApp_BareURLIsLiteral(t *testing.T) {
	for _, u := range []string{
		"http://localhost:3030/signin#__WkILRIM5Uov__-bef6HL9In0_3QKPAZQts22hS-c6k",
		"http://localhost:3030/signin#_WkILRIM5Uov_-bef6HL9In0_3QKPAZQts22hS-c6kk",
		"https://mypa.example.com/signin#_x_-_y_-__z__",
		"http://192.168.1.20:3030/a_b_/c_d_e?q=__v__#_f_",
	} {
		for _, text := range []string{u, "🔑 Link:\n\n" + u + "\n\nNever send it.", "Open " + u + " now", "(" + u + ")"} {
			got := ToWhatsApp(text)
			assert.Contains(t, got, u, "the URL is intact in %q", got)
			assert.NotContains(t, got, "*", "no bold in %q", got)
		}
	}
}

// Only bare http(s) URLs are taken literally; the rest converts as before.
func TestToWhatsApp_URLsLeaveTheRestAlone(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"emphasis around a URL", "**see** http://a.example/x__y__z _now_", "*see* http://a.example/x__y__z _now_"},
		{"markdown link", "[Docs](https://a.example/__x__)", "Docs (https://a.example/__x__)"},
		{"explicit autolink", "<http://a.example/__x__>", "http://a.example/__x__"},
		{"URL ending a sentence", "Go to http://localhost:3030/a_b_.", "Go to http://localhost:3030/a_b_."},
		{"bold word", "a __word__ here", "a *word* here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ToWhatsApp(tt.in))
		})
	}
}

// Sign-in tokens as identity.NewSecret makes them, from a fixed seed: each reaches the person
// intact.
func TestToWhatsApp_SignInTokensSurvive(t *testing.T) {
	rng := rand.New(rand.NewPCG(93, 45569046251))
	buf := make([]byte, 32)
	broken := 0
	for i := 0; i < 20000; i++ {
		for j := range buf {
			buf[j] = byte(rng.Uint32())
		}
		link := "http://localhost:3030/signin#" + base64.RawURLEncoding.EncodeToString(buf)
		if got := ToWhatsApp("🔑 Link:\n\n" + link + "\n\nNever send it."); !strings.Contains(got, link+"\n") {
			broken++
			if broken <= 3 {
				t.Errorf("link changed: %q", got)
			}
		}
	}
	assert.Zero(t, broken, "links changed by the formatting")
}
