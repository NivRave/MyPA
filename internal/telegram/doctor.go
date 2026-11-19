package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/doctor"
)

// WebhookHealth is what Telegram reports about the registered webhook (Bot API getWebhookInfo),
// as the /doctor check reads it.
type WebhookHealth struct {
	URL          string    // empty: no webhook is registered
	Pending      int       // updates Telegram is still trying to deliver
	LastErrorAt  time.Time // zero: no delivery error on record
	LastErrorMsg string
}

// Windows for judging Telegram's last delivery error.
const (
	// webhookErrorIsCurrent: an error this recent means deliveries are failing right now.
	webhookErrorIsCurrent = 15 * time.Minute
	// webhookErrorIsRelevant: an older error is still worth showing for a day; with updates
	// waiting it is a failure, without it is a warning. Older than this it is history.
	webhookErrorIsRelevant = 24 * time.Hour
)

// WebhookCheck is the /doctor check (V4-17) of the Telegram adapter, moved from internal/doctor
// with the channel layer (V4-31). It checks that Telegram delivers updates to this install: a webhook is registered,
// it points at the configured address, and Telegram is not reporting delivery errors.
type WebhookCheck struct {
	Configured bool   // a bot token is set
	Polling    bool   // no public address: the adapter polls, so no webhook may be set (V4-40)
	WantURL    string // the derived webhook address (PUBLIC_URL + /webhook/telegram); empty skips the comparison
	Info       func(ctx context.Context) (WebhookHealth, error)
	Now        func() time.Time
}

func (WebhookCheck) Name() string { return "Telegram webhook" }

func (c WebhookCheck) Run(ctx context.Context, _ doctor.Scope) []doctor.Result {
	const restart = "then restart the app, which registers the webhook when it starts."
	res := doctor.Result{Check: c.Name()}
	if !c.Configured {
		res.Status, res.Detail = doctor.StatusInfo, "Telegram is not configured."
		return []doctor.Result{res}
	}
	info, err := c.Info(ctx)
	if err != nil {
		res.Status = doctor.StatusFail
		res.Detail = fmt.Sprintf("Could not ask Telegram about the webhook: %s.", doctor.Reason(err))
		res.NextStep = "Check `TELEGRAM_BOT_TOKEN` and that this machine can reach api.telegram.org."
		return []doctor.Result{res}
	}
	if c.Polling {
		return []doctor.Result{c.pollingResult(info)}
	}
	if info.URL == "" {
		res.Status = doctor.StatusFail
		res.Detail = "No webhook is registered, so messages sent on Telegram do not arrive."
		res.NextStep = "Check that `PUBLIC_URL` is this install's public address, " + restart
		return []doctor.Result{res}
	}
	if c.WantURL != "" && info.URL != c.WantURL {
		res.Status = doctor.StatusFail
		res.Detail = fmt.Sprintf("Telegram sends updates to a different address (%s) than the configured one (%s).",
			doctor.HostOf(info.URL), doctor.HostOf(c.WantURL))
		res.NextStep = "If the public address changed, update `PUBLIC_URL`, " + restart
		return []doctor.Result{res}
	}

	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	waiting := doctor.Plural(info.Pending, "update is", "updates are") + " waiting"
	if !info.LastErrorAt.IsZero() {
		since := now.Sub(info.LastErrorAt)
		failing := since <= webhookErrorIsCurrent || (info.Pending > 0 && since <= webhookErrorIsRelevant)
		switch {
		case failing:
			res.Status = doctor.StatusFail
			res.Detail = fmt.Sprintf("Telegram could not deliver updates: %s (%s). %s.", info.LastErrorMsg, doctor.Ago(since), waiting)
			res.NextStep = "Check that `PUBLIC_URL` is reachable from the internet (is the tunnel or proxy up?) and that the app is running."
			return []doctor.Result{res}
		case since <= webhookErrorIsRelevant:
			res.Status = doctor.StatusWarn
			res.Detail = fmt.Sprintf("Deliveries work now, but Telegram reported an error %s: %s.", doctor.Ago(since), info.LastErrorMsg)
			res.NextStep = "No action if this was a restart. If it repeats, check the tunnel or proxy and the app log around that time."
			return []doctor.Result{res}
		}
	}
	res.Status = doctor.StatusOK
	res.Detail = fmt.Sprintf("Registered at %s, no recent delivery errors, %s.", doctor.HostOf(info.URL), waiting)
	return []doctor.Result{res}
}

// pollingResult judges a polling install: Telegram must have no webhook, or it delivers there and
// getUpdates is refused.
func (c WebhookCheck) pollingResult(info WebhookHealth) doctor.Result {
	res := doctor.Result{Check: c.Name()}
	waiting := doctor.Plural(info.Pending, "update is", "updates are") + " waiting"
	if info.URL != "" {
		res.Status = doctor.StatusFail
		res.Detail = fmt.Sprintf("This install polls Telegram, but a webhook is registered at %s, so updates go there instead. %s.",
			doctor.HostOf(info.URL), waiting)
		res.NextStep = "Another install is probably live on this bot, and only one install may be live per bot: stop it " +
			"(docs/RESTORE_AND_MIGRATE.md), then restart this app, which removes the webhook when it starts."
		return res
	}
	res.Status = doctor.StatusOK
	res.Detail = fmt.Sprintf("No public address: the app polls Telegram for updates. %s.", waiting)
	return res
}
