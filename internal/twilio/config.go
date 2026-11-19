package twilio

import (
	"errors"
	"net/url"
	"strings"

	"github.com/nivik/mypa/internal/deploy"
)

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
}

// LoadConfig reads the settings from the environment (getenv is os.Getenv in the app).
func LoadConfig(getenv func(string) string, site deploy.Settings) Config {
	return Config{
		AccountSID:        getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:         getenv("TWILIO_AUTH_TOKEN"),
		FromNumber:        getenv("TWILIO_FROM_NUMBER"),
		StatusCallbackURL: site.URL(StatusCallbackPath),
		PublicURL:         site.URL(""),
	}
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
