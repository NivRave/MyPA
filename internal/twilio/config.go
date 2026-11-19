package twilio

import (
	"errors"
	"net/url"
	"strings"
)

// Config holds the Twilio settings of the WhatsApp adapter. It was config.TwilioConfig before the
// channel layer (V4-31) made each adapter own its settings.
type Config struct {
	AccountSID string // TWILIO_ACCOUNT_SID
	AuthToken  string // TWILIO_AUTH_TOKEN: signs every webhook; the webhooks fail closed without it
	FromNumber string // TWILIO_FROM_NUMBER, e.g. "whatsapp:+14155238886"
	// StatusCallbackURL (optional, BL-8) is sent with every outbound message so Twilio reports
	// delivery results to the app's /webhook/twilio/status. Empty = no delivery reports.
	StatusCallbackURL string // TWILIO_STATUS_CALLBACK_URL
}

// LoadConfig reads the settings from the environment (getenv is os.Getenv in the app).
func LoadConfig(getenv func(string) string) Config {
	return Config{
		AccountSID:        getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:         getenv("TWILIO_AUTH_TOKEN"),
		FromNumber:        getenv("TWILIO_FROM_NUMBER"),
		StatusCallbackURL: getenv("TWILIO_STATUS_CALLBACK_URL"),
	}
}

// ConditionalEnvKeys are the settings Validate demands when WhatsApp is configured.
func ConditionalEnvKeys() []string { return []string{"TWILIO_AUTH_TOKEN"} }

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

// validateStatusCallback: optional (BL-8), but if set Twilio must be able to call it — an
// absolute https URL, not an .env.example placeholder.
func (c Config) validateStatusCallback() []error {
	raw := strings.TrimSpace(c.StatusCallbackURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return []error{errors.New("TWILIO_STATUS_CALLBACK_URL must be an absolute https URL (e.g. https://<public-host>/webhook/twilio/status)")}
	}
	if strings.Contains(strings.ToLower(raw), "your-") || strings.Contains(raw, "<YOUR_") {
		return []error{errors.New("TWILIO_STATUS_CALLBACK_URL still contains the .env.example placeholder value")}
	}
	return nil
}
