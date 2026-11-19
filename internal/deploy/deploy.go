// Package deploy holds how this install is reached (V4-40): the ingress setting, which says what
// lets the outside world in, and the one public address every derived address comes from. The
// same release runs on a home PC or on a server; which one is chosen here and by compose profiles,
// never by editing files.
package deploy

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Ingress names how the install is reached (INGRESS).
type Ingress string

const (
	// None: nothing reaches the install. Channels that connect outward still work (a bot that
	// polls); a channel that needs webhooks stops the app at start. The default.
	None Ingress = "none"
	// Caddy: the bundled reverse proxy with automatic TLS, for a server with its own domain.
	Caddy Ingress = "caddy"
	// Cloudflare: the bundled Cloudflare Tunnel connector.
	Cloudflare Ingress = "cloudflare"
	// Tailscale: the bundled Tailscale node, publishing the app with Funnel.
	Tailscale Ingress = "tailscale"
	// Proxy: the owner's own reverse proxy, outside the stack, forwards to the app's port.
	Proxy Ingress = "proxy"
	// Ngrok: the bundled ngrok agent.
	Ngrok Ingress = "ngrok"
)

// Ingresses returns every accepted INGRESS value, in the order the settings template lists them.
func Ingresses() []Ingress { return []Ingress{None, Caddy, Cloudflare, Tailscale, Proxy, Ngrok} }

// Service is the compose service, and the compose profile that starts it, that runs this ingress.
// It is empty when the stack runs nothing for it (None, and Proxy, which the owner runs).
func (i Ingress) Service() string {
	switch i {
	case Caddy, Cloudflare, Tailscale, Ngrok:
		return string(i)
	}
	return ""
}

// Services returns the compose services of every bundled ingress, in Ingresses order.
func Services() []string {
	var out []string
	for _, in := range Ingresses() {
		if s := in.Service(); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Settings are the deployment settings as loaded from the environment. The zero value is the
// local default: INGRESS=none, no public address.
type Settings struct {
	// Ingress is INGRESS, lower case; None when unset (the zero value counts as None too).
	Ingress Ingress
	// PublicURL is PUBLIC_URL normalised to scheme://host[:port], without a trailing slash; empty
	// when unset. It is meaningful only when Validate returns nothing.
	PublicURL string

	rawPublicURL string
	profiles     []string // COMPOSE_PROFILES as compose would read it; nil when unset
}

// Load reads the settings (getenv is os.Getenv in the app).
func Load(getenv func(string) string) Settings {
	s := Settings{Ingress: Ingress(strings.ToLower(strings.TrimSpace(getenv("INGRESS"))))}
	if s.Ingress == "" {
		s.Ingress = None
	}
	s.rawPublicURL = strings.TrimSpace(getenv("PUBLIC_URL"))
	if u, err := url.Parse(s.rawPublicURL); err == nil && u.Host != "" {
		s.PublicURL = u.Scheme + "://" + strings.ToLower(u.Host)
	}
	for _, p := range strings.Split(getenv("COMPOSE_PROFILES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			s.profiles = append(s.profiles, p)
		}
	}
	return s
}

// Reachable reports whether the outside world can reach the install, so that webhooks can be
// delivered to it.
func (s Settings) Reachable() bool {
	return s.Ingress != None && slices.Contains(Ingresses(), s.Ingress) && s.PublicURL != ""
}

// URL is the public address of path (which starts with "/"), or "" when the install is not
// reachable.
func (s Settings) URL(path string) string {
	if !s.Reachable() {
		return ""
	}
	return s.PublicURL + path
}

// Host is the host (and port, if any) of the public address, or "" when there is none.
func (s Settings) Host() string {
	if !s.Reachable() {
		return ""
	}
	return strings.TrimPrefix(s.PublicURL, "https://")
}

// Validate returns every problem with the settings at once.
func (s Settings) Validate() []error {
	if s.Ingress == "" {
		s.Ingress = None
	}
	var errs []error
	known := slices.Contains(Ingresses(), s.Ingress)
	if !known {
		names := make([]string, 0, len(Ingresses()))
		for _, in := range Ingresses() {
			names = append(names, string(in))
		}
		errs = append(errs, fmt.Errorf("INGRESS %q is not one of %s", s.Ingress, strings.Join(names, ", ")))
	}

	switch {
	case s.Ingress == None && s.rawPublicURL != "":
		errs = append(errs, errors.New("PUBLIC_URL is set but INGRESS is none, so nothing could reach that address: "+
			"set INGRESS to how the install is reached, or remove PUBLIC_URL"))
	case s.Ingress != None && known:
		errs = append(errs, s.validatePublicURL()...)
	}
	return append(errs, s.validateProfiles()...)
}

func (s Settings) validatePublicURL() []error {
	raw := s.rawPublicURL
	if raw == "" {
		return []error{fmt.Errorf("PUBLIC_URL is required with INGRESS=%s: the https address the outside world uses to reach this install, e.g. https://mypa.example.com", s.Ingress)}
	}
	if strings.Contains(raw, "<YOUR_") || strings.HasPrefix(strings.ToLower(strings.TrimPrefix(raw, "https://")), "your-") {
		return []error{errors.New("PUBLIC_URL still contains the .env.example placeholder value")}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return []error{errors.New("PUBLIC_URL must be an absolute https address, e.g. https://mypa.example.com")}
	}
	if u.User != nil {
		return []error{errors.New("PUBLIC_URL must have no user name or password in it")}
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return []error{errors.New("PUBLIC_URL must be the address alone, with no path, query or fragment: every webhook path is added to it")}
	}
	return nil
}

// validateProfiles checks that COMPOSE_PROFILES, when it is set, starts the bundled service of the
// chosen ingress and no other one: two tunnels would both forward, and a missing one would leave
// the install unreachable while it believes it has an address.
func (s Settings) validateProfiles() []error {
	if s.profiles == nil {
		return nil
	}
	var errs []error
	want := s.Ingress.Service()
	if want != "" && !slices.Contains(s.profiles, want) {
		errs = append(errs, fmt.Errorf("INGRESS=%s but COMPOSE_PROFILES does not start %q: add it to COMPOSE_PROFILES", s.Ingress, want))
	}
	for _, other := range Services() {
		if other != want && slices.Contains(s.profiles, other) {
			errs = append(errs, fmt.Errorf("INGRESS=%s but COMPOSE_PROFILES starts %q too: remove it from COMPOSE_PROFILES", s.Ingress, other))
		}
	}
	return errs
}
