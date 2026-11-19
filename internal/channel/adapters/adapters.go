// Package adapters is the list of channel adapters MyPA ships (V4-31): their settings and the one
// function that builds and registers them. It is the only code outside the adapters themselves
// that names a platform; adding a channel means adding it here, and nowhere else outside its own
// package.
package adapters

import (
	"fmt"
	"net/http"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/deploy"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
)

// Config holds the settings of every adapter.
type Config struct {
	Telegram telegram.Config
	WhatsApp twilio.Config
}

// LoadConfig reads every adapter's settings from the environment (getenv is os.Getenv in the app).
// Each adapter derives its public addresses, and whether it can receive at all without one, from
// the deployment settings (V4-40).
func LoadConfig(getenv func(string) string) Config {
	site := deploy.Load(getenv)
	return Config{
		Telegram: telegram.LoadConfig(getenv, site),
		WhatsApp: twilio.LoadConfig(getenv, site),
	}
}

// Validate checks every adapter's settings for a process that receives webhooks, runs the worker,
// or both.
func (c Config) Validate(servesIngress, runsWorker bool) []error {
	var errs []error
	errs = append(errs, c.Telegram.Validate(servesIngress)...)
	errs = append(errs, c.WhatsApp.Validate(servesIngress, runsWorker)...)
	return errs
}

// ConditionalEnvKeys are the settings Validate may demand, so .env.example must list them.
func ConditionalEnvKeys() []string {
	return append(telegram.ConditionalEnvKeys(), twilio.ConditionalEnvKeys()...)
}

// ReplacedEnvKeys are the adapters' settings that PUBLIC_URL replaced (V4-40). The app refuses to
// start while one is still set, so an old value can never silently disagree with the new one.
func ReplacedEnvKeys() []string {
	return append(telegram.ReplacedEnvKeys(), twilio.ReplacedEnvKeys()...)
}

// options are the test seams of Build.
type options struct {
	httpClient  *http.Client
	telegramAPI telegram.API
	whatsAppAPI twilio.API
}

// Option configures Build.
type Option func(*options)

// WithHTTPClient makes every adapter's platform client use hc: the conformance suite (V4-34)
// passes a fake transport, so nothing can reach a real platform.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithAPIs replaces the platform clients the adapters send and fetch with (mocks in the engine's
// tests). A nil value keeps that adapter's real client.
func WithAPIs(tg telegram.API, wa twilio.API) Option {
	return func(o *options) { o.telegramAPI, o.whatsAppAPI = tg, wa }
}

// Build constructs every adapter and registers it. Both are always registered, configured or not:
// an unconfigured adapter's webhooks reject every request, as the ingress always did, and /doctor
// reports it as not configured. The registry is sealed before it is returned, so this list is the
// only way an adapter reaches the app, and the channel conformance suite (internal/conformance,
// V4-34) runs every scenario on every adapter in it: a new adapter cannot ship without passing.
func Build(cfg Config, opts ...Option) (*channel.Registry, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	var tgOpts []telegram.AdapterOption
	var waOpts []twilio.AdapterOption
	if o.httpClient != nil {
		tgOpts = append(tgOpts, telegram.WithClient(telegram.NewClient(cfg.Telegram.BotToken, telegram.WithHTTPClient(o.httpClient))))
		waOpts = append(waOpts, twilio.WithAPI(twilio.NewClient(cfg.WhatsApp, twilio.WithHTTPClient(o.httpClient))))
	}
	if o.telegramAPI != nil {
		tgOpts = append(tgOpts, telegram.WithAPI(o.telegramAPI))
	}
	if o.whatsAppAPI != nil {
		waOpts = append(waOpts, twilio.WithAPI(o.whatsAppAPI))
	}

	reg := channel.NewRegistry()
	for _, a := range []channel.Adapter{
		telegram.NewAdapter(cfg.Telegram, tgOpts...),
		twilio.NewAdapter(cfg.WhatsApp, waOpts...),
	} {
		if err := reg.Register(a); err != nil {
			return nil, fmt.Errorf("registering channel adapters: %w", err)
		}
	}
	reg.Seal()
	return reg, nil
}
