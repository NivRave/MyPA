package twilio

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/nivik/mypa/internal/deploy"
)

// Mode is how this install sends WhatsApp messages (V4-41, DE-8).
type Mode string

const (
	// ModeSandbox is Twilio's shared sandbox number: each person first sends the sandbox's join
	// message, their membership lapses 72 hours after they last wrote, and only Twilio's stock
	// templates exist, so MyPA has none of its own.
	ModeSandbox Mode = "sandbox"
	// ModeSender is a WhatsApp sender registered in the owner's own Twilio account: no join step,
	// and MyPA's reminder and briefing templates once the owner has had them approved.
	ModeSender Mode = "sender"
)

// Modes lists every mode, in the order the docs and the conformance suite show them.
func Modes() []Mode { return []Mode{ModeSandbox, ModeSender} }

// contentSID is the form of a Twilio Content API SID (a template).
var contentSID = regexp.MustCompile(`^HX[0-9a-fA-F]{32}$`)

// Config holds the Twilio settings of the WhatsApp adapter. It was config.TwilioConfig before the
// channel layer (V4-31) made each adapter own its settings.
type Config struct {
	AccountSID string // TWILIO_ACCOUNT_SID
	AuthToken  string // TWILIO_AUTH_TOKEN: signs every webhook; the webhooks fail closed without it
	FromNumber string // TWILIO_FROM_NUMBER, e.g. "whatsapp:+14155238886"
	// StatusCallbackURL (BL-8) is sent with every outbound message so Twilio reports delivery
	// results to the app's /webhook/twilio/status. It is PUBLIC_URL followed by that path (V4-40),
	// derived, never set on its own; empty without a public address.
	StatusCallbackURL string
	// PublicURL is the install's public address (PUBLIC_URL, V4-40). Twilio signs the address it
	// calls, so the webhooks verify signatures against it. Empty: rebuilt from the request.
	PublicURL string

	// Mode is TWILIO_WHATSAPP_MODE (V4-41). Unset, it follows the number: the sandbox number (or
	// none) is ModeSandbox, any other ModeSender. LoadConfig resolves it; Effective does for a
	// Config built by hand.
	Mode Mode
	// modeSetting is TWILIO_WHATSAPP_MODE as written, kept so Validate can refuse an unknown value.
	modeSetting string
	// SandboxJoin is the message a person sends to the sandbox number to join it, "join <code>"
	// (TWILIO_SANDBOX_JOIN_CODE, written with or without "join"). Empty: invitations say where to
	// find it.
	SandboxJoin string
	// TemplateSIDs maps a template key (TemplateReminder, TemplateBriefing) to the Content SID of
	// the owner's template (TWILIO_TEMPLATE_REMINDER_SID, TWILIO_TEMPLATE_BRIEFING_SID). Sender
	// mode only; /doctor reports their approval, and sending with them is V4-33.
	TemplateSIDs map[string]string
}

// templateEnvKeys are the settings that hold each template's Content SID.
var templateEnvKeys = map[string]string{
	TemplateReminder: "TWILIO_TEMPLATE_REMINDER_SID",
	TemplateBriefing: "TWILIO_TEMPLATE_BRIEFING_SID",
}

// LoadConfig reads the settings from the environment (getenv is os.Getenv in the app).
func LoadConfig(getenv func(string) string, site deploy.Settings) Config {
	c := Config{
		AccountSID:        getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:         getenv("TWILIO_AUTH_TOKEN"),
		FromNumber:        getenv("TWILIO_FROM_NUMBER"),
		StatusCallbackURL: site.URL(StatusCallbackPath),
		PublicURL:         site.URL(""),
		modeSetting:       getenv("TWILIO_WHATSAPP_MODE"),
		SandboxJoin:       joinMessage(getenv("TWILIO_SANDBOX_JOIN_CODE")),
		TemplateSIDs:      map[string]string{},
	}
	for key, env := range templateEnvKeys {
		if sid := strings.TrimSpace(getenv(env)); sid != "" {
			c.TemplateSIDs[key] = sid
		}
	}
	return c.Effective()
}

// Effective returns the settings with the mode resolved: the one set, or else the one the number
// implies. A value Validate refuses also resolves by the number, so every adapter has a mode.
func (c Config) Effective() Config {
	if c.Mode != "" {
		return c
	}
	switch m := parseMode(c.modeSetting); m {
	case ModeSandbox, ModeSender:
		c.Mode = m
	default:
		c.Mode = ModeSender
		if strings.TrimSpace(c.FromNumber) == "" || isSandboxNumber(c.FromNumber) {
			c.Mode = ModeSandbox
		}
	}
	return c
}

func parseMode(raw string) Mode { return Mode(strings.ToLower(strings.TrimSpace(raw))) }

// isSandboxNumber reports whether a sender is Twilio's shared sandbox number.
func isSandboxNumber(from string) bool {
	return strings.EqualFold(strings.TrimSpace(from), SandboxSender)
}

// joinMessage writes a join code as the message to send: "plenty-tiger" and "join plenty-tiger"
// both become "join plenty-tiger".
func joinMessage(raw string) string {
	code := strings.TrimSpace(raw)
	if code == "" {
		return ""
	}
	if f := strings.Fields(code); len(f) > 1 && strings.EqualFold(f[0], "join") {
		code = strings.TrimSpace(code[len(f[0]):])
	}
	return "join " + code
}

// ConditionalEnvKeys are the settings Validate demands when WhatsApp is configured.
func ConditionalEnvKeys() []string { return []string{"TWILIO_AUTH_TOKEN"} }

// ReplacedEnvKeys are settings this adapter no longer reads: PUBLIC_URL replaced them (V4-40).
func ReplacedEnvKeys() []string { return []string{"TWILIO_STATUS_CALLBACK_URL"} }

// Validate checks the settings for a process that receives webhooks, runs the worker, or both.
func (c Config) Validate(servesIngress, runsWorker bool) []error {
	var errs []error
	if (servesIngress || runsWorker) && c.AccountSID != "" && c.AuthToken == "" {
		errs = append(errs, errors.New("TWILIO_AUTH_TOKEN is required when TWILIO_ACCOUNT_SID is set (webhook signatures cannot be verified)"))
	}
	if runsWorker {
		errs = append(errs, c.validateStatusCallback()...)
	}
	if (servesIngress || runsWorker) && c.AccountSID != "" {
		errs = append(errs, c.validateMode()...)
	}
	return errs
}

// validateMode refuses mode settings that cannot work (V4-41). Values are never quoted back, so a
// secret pasted into the wrong setting does not reach the log.
func (c Config) validateMode() []error {
	var errs []error
	switch parseMode(c.modeSetting) {
	case "", ModeSandbox, ModeSender:
	default:
		errs = append(errs, errors.New("TWILIO_WHATSAPP_MODE must be sandbox or sender (or unset, to follow TWILIO_FROM_NUMBER)"))
	}
	if c.Effective().Mode == ModeSender && isSandboxNumber(c.FromNumber) {
		errs = append(errs, errors.New("TWILIO_WHATSAPP_MODE is sender but TWILIO_FROM_NUMBER is Twilio's sandbox number: set the registered sender's number, or use sandbox"))
	}
	if strings.ContainsAny(c.SandboxJoin, "\r\n") {
		errs = append(errs, errors.New(`TWILIO_SANDBOX_JOIN_CODE must be one line: the code Twilio shows after "join"`))
	}
	for _, key := range []string{TemplateReminder, TemplateBriefing} {
		if sid, ok := c.TemplateSIDs[key]; ok && !contentSID.MatchString(sid) {
			errs = append(errs, fmt.Errorf("%s must be the template's Content SID (HX followed by 32 hex digits)", templateEnvKeys[key]))
		}
	}
	return errs
}

// validateStatusCallback: derived from PUBLIC_URL, which is validated itself (V4-40); kept so a
// configuration built another way still cannot hand Twilio an address it cannot call.
func (c Config) validateStatusCallback() []error {
	raw := strings.TrimSpace(c.StatusCallbackURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return []error{errors.New("the WhatsApp delivery-report address (PUBLIC_URL + /webhook/twilio/status) must be an absolute https URL")}
	}
	if strings.Contains(strings.ToLower(raw), "your-") || strings.Contains(raw, "<YOUR_") {
		return []error{errors.New("the WhatsApp delivery-report address (PUBLIC_URL + /webhook/twilio/status) still contains the .env.example placeholder value")}
	}
	return nil
}
