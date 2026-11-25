package telegram

import (
	"encoding/base64"
	"html"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A bare URL reaches the person exactly as written: underscores or asterisks in it are never read
// as emphasis. A dashboard sign-in token is random base64url, so "#_abc_-" or "-__x__" turns up in
// some links; read as emphasis it became "<i>abc</i>", the underscores were lost and the link no
// longer signed anyone in (CI run 45569046251, TestDashboard_OnlyOwnersAdultsAndTheOperator).
func TestToTelegramHTML_BareURLIsLiteral(t *testing.T) {
	urls := []string{
		// The token from the failed CI run.
		"http://localhost:3030/signin#_WkILRIM5Uov_-bef6HL9In0_3QKPAZQts22hS-c6kk",
		"http://localhost:3030/signin#__WkILRIM5Uov__-bef6HL9In0_3QKPAZQts22hS-c6k",
		"http://localhost:3030/signin#abc-_WkILRIM5Uov_-bef6HL9In0_3QKPAZQts22hS-c",
		"http://localhost:3030/signin#WkILRIM5Uov-bef6HL9In0_3QKPAZQts22hS-c6kkab_",
		"https://mypa.example.com/signin#_x_-_y_-__z__",
		"http://192.168.1.20:3030/a_b_/c_d_e?q=_v_#_f_",
	}
	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			for _, text := range []string{
				u,
				"🔑 Your dashboard sign-in link:\n\n" + u + "\n\nNever send it to anyone.",
				"Open " + u + " now",
				"(" + u + ")",
			} {
				got := ToTelegramHTML(text)
				assert.Contains(t, got, html.EscapeString(u), "the URL is intact in %q", got)
				assert.NotContains(t, got, "<i>", "no emphasis in %q", got)
				assert.NotContains(t, got, "<b>", "no emphasis in %q", got)
			}
		})
	}
}

// Only bare http(s) URLs are taken literally; the rest of the markdown is converted as before.
func TestToTelegramHTML_URLsLeaveTheRestAlone(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"emphasis around a URL", "*see* http://a.example/x_y_z **now**", "<i>see</i> http://a.example/x_y_z <b>now</b>"},
		{"explicit autolink", "<http://a.example/_x_>", `<a href="http://a.example/_x_">http://a.example/_x_</a>`},
		{"URL ending a sentence", "Go to http://localhost:3030/a_b_.", "Go to http://localhost:3030/a_b_."},
		{"markdown link", "[Docs](https://a.example/_x_)", `<a href="https://a.example/_x_">Docs</a>`},
		{"URL in a code span", "`http://a.example/_x_`", "<code>http://a.example/_x_</code>"},
		{"email stays text", "write to a_b_@example.com", "write to a_b_@example.com"},
		{"www stays text", "see www.example.com", "see www.example.com"},
		{"snake_case word", "a _word_ here", "a <i>word</i> here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, strings.TrimSpace(ToTelegramHTML(tt.in)))
		})
	}
}

// Sign-in tokens as identity.NewSecret makes them (32 random bytes, base64url), from a fixed seed
// so the run is the same every time: each one reaches the person intact.
func TestToTelegramHTML_SignInTokensSurvive(t *testing.T) {
	rng := rand.New(rand.NewPCG(93, 45569046251))
	buf := make([]byte, 32)
	broken := 0
	for i := 0; i < 20000; i++ {
		for j := range buf {
			buf[j] = byte(rng.Uint32())
		}
		link := "http://localhost:3030/signin#" + base64.RawURLEncoding.EncodeToString(buf)
		got := ToTelegramHTML("🔑 Your dashboard sign-in link, valid for 10 minutes and once:\n\n" + link + "\n\nNever send it to anyone.")
		if !strings.Contains(got, link+"\n") {
			broken++
			if broken <= 3 {
				t.Errorf("link changed: %q", got)
			}
		}
	}
	assert.Zero(t, broken, "links changed by the formatting")
}
