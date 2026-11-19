package deploy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func errText(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}

// An install with no settings at all is the local default: nothing reaches it, which is what a
// Telegram-only install with no tunnel needs.
func TestLoad_DefaultIsLocalWithNoPublicAddress(t *testing.T) {
	s := Load(env(nil))
	assert.Equal(t, None, s.Ingress)
	assert.False(t, s.Reachable())
	assert.Empty(t, s.PublicURL)
	assert.Empty(t, s.URL("/webhook/x"), "no public address, so no derived address either")
	assert.Empty(t, s.Validate())
}

func TestLoad_DerivesEveryAddressFromThePublicURL(t *testing.T) {
	for _, raw := range []string{"https://mypa.example.com", "https://mypa.example.com/", " https://MyPA.example.com "} {
		s := Load(env(map[string]string{"INGRESS": "caddy", "PUBLIC_URL": raw}))
		require.Empty(t, s.Validate(), raw)
		assert.True(t, s.Reachable())
		assert.Equal(t, "https://mypa.example.com", s.PublicURL, "normalised: no trailing slash, lower-case host")
		assert.Equal(t, "https://mypa.example.com/webhook/x", s.URL("/webhook/x"))
		assert.Equal(t, "mypa.example.com", s.Host())
	}
}

func TestLoad_IngressIsCaseInsensitive(t *testing.T) {
	s := Load(env(map[string]string{"INGRESS": " Ngrok ", "PUBLIC_URL": "https://a.ngrok-free.dev"}))
	assert.Equal(t, Ngrok, s.Ingress)
	assert.Empty(t, s.Validate())
}

func TestValidate_EveryIngressThatReachesTheInstallNeedsAPublicURL(t *testing.T) {
	for _, in := range Ingresses() {
		if in == None {
			continue
		}
		errs := Load(env(map[string]string{"INGRESS": string(in)})).Validate()
		assert.Contains(t, errText(errs), "PUBLIC_URL is required", in)
	}
}

func TestValidate_RefusesAPublicURLThatCannotBeTheInstallsAddress(t *testing.T) {
	for raw, want := range map[string]string{
		"http://mypa.example.com":          "https",
		"mypa.example.com":                 "https",
		"https://":                         "https",
		"https://mypa.example.com/mypa":    "no path",
		"https://mypa.example.com/?a=1":    "no path",
		"https://mypa.example.com/#x":      "no path",
		"https://user:pw@mypa.example.com": "no user",
		"https://your-domain.example.com":  "placeholder",
		"https://<YOUR_DOMAIN>":            "placeholder",
	} {
		errs := Load(env(map[string]string{"INGRESS": "proxy", "PUBLIC_URL": raw})).Validate()
		assert.Contains(t, errText(errs), want, raw)
	}
}

func TestValidate_NoneWithAPublicURLContradictsItself(t *testing.T) {
	errs := Load(env(map[string]string{"PUBLIC_URL": "https://mypa.example.com"})).Validate()
	assert.Contains(t, errText(errs), "INGRESS is none")
}

func TestValidate_RefusesAnUnknownIngress(t *testing.T) {
	errs := Load(env(map[string]string{"INGRESS": "traefik", "PUBLIC_URL": "https://mypa.example.com"})).Validate()
	text := errText(errs)
	assert.Contains(t, text, `INGRESS "traefik"`)
	for _, in := range Ingresses() {
		assert.Contains(t, text, string(in), "the message lists the choices")
	}
	assert.False(t, Load(env(map[string]string{"INGRESS": "traefik"})).Reachable(), "an unknown ingress derives no address")
}

// The bundled tunnel or proxy runs as the compose service and profile named after the ingress, so
// COMPOSE_PROFILES in .env must start that one and no other. Without COMPOSE_PROFILES (profiles
// chosen on the command line) the app cannot see them and does not guess.
func TestValidate_ComposeProfilesMustStartTheChosenIngress(t *testing.T) {
	cases := []struct {
		ingress, profiles, want string
	}{
		{"ngrok", "ngrok", ""},
		{"ngrok", "tools, ngrok", ""},
		{"ngrok", "", ""},
		{"caddy", "tools", `COMPOSE_PROFILES does not start "caddy"`},
		{"caddy", "caddy,ngrok", `"ngrok" too`},
		{"proxy", "tools", ""},
		{"proxy", "cloudflare", `"cloudflare" too`},
		{"none", "tailscale", `"tailscale" too`},
	}
	for _, c := range cases {
		kv := map[string]string{"INGRESS": c.ingress, "COMPOSE_PROFILES": c.profiles}
		if c.ingress != "none" {
			kv["PUBLIC_URL"] = "https://mypa.example.com"
		}
		text := errText(Load(env(kv)).Validate())
		if c.want == "" {
			assert.Empty(t, text, "%s with %q", c.ingress, c.profiles)
		} else {
			assert.Contains(t, text, c.want, "%s with %q", c.ingress, c.profiles)
		}
	}
}

func TestIngress_ServiceIsTheComposeServiceAndProfile(t *testing.T) {
	assert.Equal(t, "", None.Service(), "nothing to run")
	assert.Equal(t, "", Proxy.Service(), "the owner's own proxy is not part of the stack")
	for _, in := range []Ingress{Caddy, Cloudflare, Tailscale, Ngrok} {
		assert.Equal(t, string(in), in.Service())
	}
	assert.Equal(t, []string{"caddy", "cloudflare", "tailscale", "ngrok"}, Services())
}

func TestValidate_ReportsEveryProblemAtOnce(t *testing.T) {
	errs := Load(env(map[string]string{"INGRESS": "caddy", "PUBLIC_URL": "http://x", "COMPOSE_PROFILES": "ngrok"})).Validate()
	assert.GreaterOrEqual(t, len(errs), 2)
}
