package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/models"
	"golang.org/x/oauth2"
)

// --- Telegram webhook -------------------------------------------------------------------------

// WebhookInfo is what Telegram reports about the registered webhook (Bot API getWebhookInfo).
type WebhookInfo struct {
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

// TelegramWebhook checks that Telegram delivers updates to this install: a webhook is registered,
// it points at the configured address, and Telegram is not reporting delivery errors.
type TelegramWebhook struct {
	Configured bool   // a bot token is set
	WantURL    string // TELEGRAM_WEBHOOK_URL; empty skips the address comparison
	Info       func(ctx context.Context) (WebhookInfo, error)
	Now        func() time.Time
}

func (TelegramWebhook) Name() string { return "Telegram webhook" }

func (c TelegramWebhook) Run(ctx context.Context, _ Scope) []Result {
	const restart = "then restart the gateway, which registers the webhook when it starts."
	res := Result{Check: c.Name()}
	if !c.Configured {
		res.Status, res.Detail = StatusInfo, "Telegram is not configured."
		return []Result{res}
	}
	info, err := c.Info(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not ask Telegram about the webhook: %s.", reason(err))
		res.NextStep = "Check `TELEGRAM_BOT_TOKEN` and that this machine can reach api.telegram.org."
		return []Result{res}
	}
	if info.URL == "" {
		res.Status = StatusFail
		res.Detail = "No webhook is registered, so messages sent on Telegram do not arrive."
		res.NextStep = "Set `TELEGRAM_WEBHOOK_URL` to the public address of `/webhook/telegram`, " + restart
		return []Result{res}
	}
	if c.WantURL != "" && info.URL != c.WantURL {
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Telegram sends updates to a different address (%s) than the configured one (%s).",
			hostOf(info.URL), hostOf(c.WantURL))
		res.NextStep = "If the public address changed, update `TELEGRAM_WEBHOOK_URL`, " + restart
		return []Result{res}
	}

	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	waiting := plural(info.Pending, "update is", "updates are") + " waiting"
	if !info.LastErrorAt.IsZero() {
		since := now.Sub(info.LastErrorAt)
		failing := since <= webhookErrorIsCurrent || (info.Pending > 0 && since <= webhookErrorIsRelevant)
		switch {
		case failing:
			res.Status = StatusFail
			res.Detail = fmt.Sprintf("Telegram could not deliver updates: %s (%s). %s.", info.LastErrorMsg, ago(since), waiting)
			res.NextStep = "Check that the address in `TELEGRAM_WEBHOOK_URL` is reachable from the internet (is the tunnel up?) and that the gateway is running."
			return []Result{res}
		case since <= webhookErrorIsRelevant:
			res.Status = StatusWarn
			res.Detail = fmt.Sprintf("Deliveries work now, but Telegram reported an error %s: %s.", ago(since), info.LastErrorMsg)
			res.NextStep = "No action if this was a restart. If it repeats, check the tunnel and the gateway log around that time."
			return []Result{res}
		}
	}
	res.Status = StatusOK
	res.Detail = fmt.Sprintf("Registered at %s, no recent delivery errors, %s.", hostOf(info.URL), waiting)
	return []Result{res}
}

// reason renders an error for a sentence that ends with its own full stop.
func reason(err error) string {
	return strings.TrimRight(strings.TrimSpace(err.Error()), ".")
}

// hostOf shortens a URL to its host for display (the path can carry nothing useful to the admin).
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// --- Google accounts --------------------------------------------------------------------------

// GoogleAccounts checks, for every member of the admin's family group, whether their Google
// sign-in still works: a stored token exists, can be read, and Google agrees to renew it.
type GoogleAccounts struct {
	Configured bool // a Google OAuth client is set up
	// FamilyMembers lists the users of one family group. It is never called with an empty group.
	FamilyMembers func(familyGroup string) ([]models.User, error)
	// Token returns a user's stored Google token as JSON, or nil when they never connected.
	Token func(ctx context.Context, userID string) ([]byte, error)
	// Refresh asks Google to renew the token and returns an error when it refuses (see RefreshWith).
	Refresh func(ctx context.Context, token *oauth2.Token) error
	// MemberTimeout bounds the check of one member; DefaultMemberTimeout when zero.
	MemberTimeout time.Duration
}

// Why a renewal failed. Refresh implementations wrap one of these; any other error means the
// question could not be put to Google at all.
var (
	// ErrSignInRefused: Google refused to renew this member's sign-in (expired or revoked).
	ErrSignInRefused = errors.New("google refused to renew the sign-in")
	// ErrRenewalRejected: Google rejected the request itself, which points at this install's
	// OAuth client settings rather than at the member.
	ErrRenewalRejected = errors.New("google rejected the renewal request")
)

const (
	// DefaultMemberTimeout bounds one member's check (one request to Google).
	DefaultMemberTimeout = 6 * time.Second
	// googleChecksInParallel limits concurrent requests to Google's token endpoint.
	googleChecksInParallel = 4
)

func (GoogleAccounts) Name() string { return "Google accounts" }

func (c GoogleAccounts) Run(ctx context.Context, scope Scope) []Result {
	if !c.Configured {
		return []Result{{Check: c.Name(), Status: StatusInfo, Detail: "Google sign-in is not configured."}}
	}
	if scope.Admin == nil {
		return []Result{{Check: c.Name(), Status: StatusFail, Detail: "Could not tell whose family to check."}}
	}

	// Tenant scope: the admin's own family group. An admin without one is their own tenant, as
	// everywhere else in MyPA, so only they are checked.
	members := []models.User{*scope.Admin}
	if group := strings.TrimSpace(scope.Admin.FamilyGroup); group != "" {
		listed, err := c.FamilyMembers(group)
		if err != nil {
			return []Result{{
				Check:    c.Name(),
				Status:   StatusFail,
				Detail:   "Could not read the list of family members.",
				NextStep: "Check that the database is reachable (see the schema check), then run /doctor again.",
			}}
		}
		members = listed
	}

	// Members are checked side by side, each with their own time limit, so one sign-in that
	// Google is slow to answer for cannot use up the time of the others.
	timeout := c.MemberTimeout
	if timeout <= 0 {
		timeout = DefaultMemberTimeout
	}
	results := make([]Result, len(members))
	slots := make(chan struct{}, googleChecksInParallel)
	var wg sync.WaitGroup
	for i, m := range members {
		wg.Add(1)
		go func(i int, m models.User) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("doctor google account check panicked", "panic", r)
					results[i] = Result{Check: "Google account: " + displayName(m), Status: StatusFail, Detail: "The check itself crashed."}
				}
			}()
			memberCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[i] = c.checkMember(memberCtx, m)
		}(i, m)
	}
	wg.Wait()
	return results
}

func (c GoogleAccounts) checkMember(ctx context.Context, m models.User) Result {
	who := displayName(m)
	res := Result{Check: "Google account: " + who}
	reconnect := fmt.Sprintf("Ask %s to send /connect and sign in to Google again.", who)

	raw, err := c.Token(ctx, m.PlatformID)
	if err != nil {
		res.Status = StatusFail
		res.Detail = "The stored Google sign-in cannot be read."
		res.NextStep = reconnect
		return res
	}
	if len(raw) == 0 {
		res.Status = StatusInfo
		res.Detail = "Not connected (calendar, mail, tasks and contacts are off until they send /connect)."
		return res
	}
	var token oauth2.Token
	if err := json.Unmarshal(raw, &token); err != nil {
		res.Status = StatusFail
		res.Detail = "The stored Google sign-in cannot be read."
		res.NextStep = reconnect
		return res
	}
	if token.RefreshToken == "" {
		res.Status = StatusFail
		res.Detail = "The Google sign-in cannot be renewed (no refresh token was stored), so it stops working within the hour."
		res.NextStep = reconnect
		return res
	}
	if err := c.Refresh(ctx, &token); err != nil {
		res.Status = StatusFail
		switch {
		case errors.Is(err, ErrSignInRefused):
			// Only this case is the member's to fix.
			res.Detail = "The Google sign-in has expired or was revoked: Google refused to renew it."
			res.NextStep = reconnect
		case errors.Is(err, ErrRenewalRejected):
			slog.Warn("doctor: google rejected the renewal request", "error", err)
			res.Detail = "Google rejected this install's own credentials when asked to renew the sign-in, so its state is unknown."
			res.NextStep = "Compare `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` with the OAuth client in the Google Cloud console, then run /doctor again."
		default:
			slog.Warn("doctor: could not reach google to check a sign-in", "error", err)
			res.Detail = "Could not reach Google to check the sign-in, so its state is unknown."
			res.NextStep = "Check that this machine can reach oauth2.googleapis.com, then run /doctor again."
		}
		return res
	}
	res.Status = StatusOK
	res.Detail = "Connected, and Google renews the sign-in."
	return res
}

// displayName names a member in the report without exposing a phone number: the stored name, or
// for members who never got one (stored as "Unknown") the last four characters of their ID.
func displayName(u models.User) string {
	name := strings.TrimSpace(u.Name)
	if name != "" && !strings.EqualFold(name, "unknown") {
		return name
	}
	return "member " + maskTail(u.PlatformID)
}

// maskTail keeps only the last four characters of an identifier.
func maskTail(id string) string {
	r := []rune(strings.TrimSpace(id))
	if len(r) <= 4 {
		return "…" + string(r)
	}
	return "…" + string(r[len(r)-4:])
}

// RefreshWith builds the production Refresh for GoogleAccounts from an OAuth token source
// (oauth2.Config.TokenSource or calendar.OAuthConfig.TokenSource). It forces a renewal even when
// the stored access token still looks valid, because that is the only way to learn that the
// refresh token itself has expired or been revoked. The renewed token is discarded and the
// caller's token is left untouched; Google does not invalidate the old one.
//
// The error says why the renewal failed: ErrSignInRefused when Google refused this sign-in
// (invalid_grant), ErrRenewalRejected when it rejected the request for another reason, and the
// underlying error otherwise (Google unreachable, a timeout, a server error on Google's side).
func RefreshWith(source func(ctx context.Context, token *oauth2.Token) oauth2.TokenSource) func(ctx context.Context, token *oauth2.Token) error {
	return func(ctx context.Context, token *oauth2.Token) error {
		expired := *token
		expired.Expiry = time.Now().Add(-time.Hour)
		_, err := source(ctx, &expired).Token()
		if err == nil {
			return nil
		}
		var answered *oauth2.RetrieveError
		if !errors.As(err, &answered) {
			return err
		}
		if answered.ErrorCode == "invalid_grant" {
			return fmt.Errorf("%w (invalid_grant)", ErrSignInRefused)
		}
		if answered.Response != nil && answered.Response.StatusCode >= 500 {
			return err // Google's own trouble: neither the sign-in nor our credentials
		}
		code := answered.ErrorCode
		if code == "" && answered.Response != nil {
			code = answered.Response.Status
		}
		return fmt.Errorf("%w (%s)", ErrRenewalRejected, code)
	}
}

// --- WhatsApp ---------------------------------------------------------------------------------

// TwilioSandboxSender is the shared number of Twilio's WhatsApp sandbox.
const TwilioSandboxSender = "whatsapp:+14155238886"

// WhatsApp reports how WhatsApp is set up. Until the mode setting of V4-41 exists it reads the
// configuration only: whether the sender is Twilio's shared sandbox number, and whether delivery
// reports are switched on. It does not ask Twilio about the sender.
type WhatsApp struct {
	Configured        bool   // a Twilio account is set
	Sender            string // TWILIO_FROM_NUMBER
	StatusCallbackURL string // TWILIO_STATUS_CALLBACK_URL
}

func (WhatsApp) Name() string { return "WhatsApp" }

func (c WhatsApp) Run(context.Context, Scope) []Result {
	if !c.Configured {
		return []Result{{Check: c.Name(), Status: StatusInfo, Detail: "WhatsApp is not configured."}}
	}
	var results []Result
	if strings.EqualFold(strings.TrimSpace(c.Sender), TwilioSandboxSender) {
		results = append(results, Result{
			Check:  "WhatsApp sender",
			Status: StatusWarn,
			Detail: "WhatsApp runs on the Twilio sandbox. A person who has not written for 72 hours drops out of it, " +
				"and replies and reminders to them then fail (Twilio error 63015).",
			NextStep: "When someone stops getting answers, have them send the sandbox join message again. " +
				"A registered sender removes the limit (planned as V4-41).",
		})
	}
	if strings.TrimSpace(c.StatusCallbackURL) == "" {
		results = append(results, Result{
			Check:    "WhatsApp delivery reports",
			Status:   StatusWarn,
			Detail:   "Delivery failures are not reported back, so a message Twilio accepted but could not deliver goes unnoticed.",
			NextStep: "Set `TWILIO_STATUS_CALLBACK_URL` to the public address of `/webhook/twilio/status` and restart the orchestrator.",
		})
	}
	if len(results) == 0 {
		results = append(results, Result{
			Check:  c.Name(),
			Status: StatusOK,
			Detail: fmt.Sprintf("Configured with sender %s; delivery reports are on.", maskTail(c.Sender)),
		})
	}
	return results
}

// --- Queues -----------------------------------------------------------------------------------

// Queues checks each work queue for a backlog and its dead-letter queue for failed messages.
type Queues struct {
	Names []string // primary queue names; each has a "<name>.dlq"
	// BacklogWarn is the number of waiting messages from which a queue counts as backed up.
	BacklogWarn int
	// Depth returns the number of messages ready in a queue.
	Depth func(ctx context.Context, queue string) (int, error)
}

func (Queues) Name() string { return "Queues" }

func (c Queues) Run(ctx context.Context, _ Scope) []Result {
	results := make([]Result, 0, len(c.Names))
	for _, name := range c.Names {
		res := Result{Check: "Queue " + name}
		waiting, err := c.Depth(ctx, name)
		var failed int
		if err == nil {
			failed, err = c.Depth(ctx, name+".dlq")
		}
		switch {
		case err != nil:
			res.Status = StatusFail
			res.Detail = fmt.Sprintf("Could not read the queue: %s.", reason(err))
			res.NextStep = "Check that RabbitMQ is running and that `RABBITMQ_URL` is right."
		case failed > 0:
			res.Status = StatusFail
			res.Detail = fmt.Sprintf("%s failed for good and %s kept in `%s.dlq`. %s waiting.",
				plural(failed, "message", "messages"), isAre(failed), name, plural(waiting, "message is", "messages are"))
			res.NextStep = fmt.Sprintf("Open the RabbitMQ management page, read the `x-last-error` header of the messages in `%s.dlq`, "+
				"fix the cause, then move them back to `%s` or purge them.", name, name)
		case c.BacklogWarn > 0 && waiting >= c.BacklogWarn:
			res.Status = StatusWarn
			res.Detail = fmt.Sprintf("%s waiting, which is more than usual.", plural(waiting, "message is", "messages are"))
			res.NextStep = "Check that the service reading this queue is running and not stuck (orchestrator for inbound.messages, audit-worker for audit.events)."
		default:
			res.Status = StatusOK
			res.Detail = fmt.Sprintf("%d waiting, none failed.", waiting)
		}
		results = append(results, res)
	}
	return results
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// --- Backups ----------------------------------------------------------------------------------

// Backups checks the age of the newest database backup.
type Backups struct {
	// Latest returns the file name and time of the newest backup.
	Latest func() (name string, takenAt time.Time, err error)
	// MaxAge is how old the newest backup may be. Backups run nightly, so a little over a day.
	MaxAge time.Duration
	Now    func() time.Time
}

func (Backups) Name() string { return "Backups" }

func (c Backups) Run(context.Context, Scope) []Result {
	const localOnly = "Backups are kept on this machine only; there is no off-machine copy."
	res := Result{Check: c.Name()}
	name, takenAt, err := c.Latest()
	if err != nil || name == "" {
		res.Status = StatusFail
		res.Detail = "No backup was found."
		res.NextStep = "Send /backup to take one now. If it fails, check the backup volume and `BACKUP_AGE_RECIPIENTS`."
		return []Result{res}
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	age := now.Sub(takenAt)
	switch {
	case age > c.MaxAge:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("The newest backup was taken %s (`%s`), so the nightly backup is not running.", ago(age), name)
		res.NextStep = "Send /backup to take one now, then look for \"Scheduled backup\" in the orchestrator log around 02:00 to see why the nightly run failed."
	case !strings.HasSuffix(name, ".age"):
		res.Status = StatusWarn
		res.Detail = fmt.Sprintf("The newest backup was taken %s, but it is not encrypted (`%s`). %s", ago(age), name, localOnly)
		res.NextStep = "Set `BACKUP_AGE_RECIPIENTS` to an age public key so backups are encrypted, and keep the matching private key somewhere safe."
	default:
		res.Status = StatusOK
		res.Detail = fmt.Sprintf("The newest backup was taken %s (`%s`). %s", ago(age), name, localOnly)
	}
	return []Result{res}
}

// --- Database schema --------------------------------------------------------------------------

// Schema checks that the database schema is at the version this build expects.
type Schema struct {
	// Version returns the version the database is at and the latest version this build knows.
	Version func(ctx context.Context) (current, latest int64, err error)
}

func (Schema) Name() string { return "Database schema" }

func (c Schema) Run(ctx context.Context, _ Scope) []Result {
	res := Result{Check: c.Name()}
	current, latest, err := c.Version(ctx)
	switch {
	case err != nil:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not read the schema version: %s.", reason(err))
		res.NextStep = "Check that Postgres is running and that `DATABASE_URL` is right."
	case current < latest:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("The database is at version %d, but this build expects version %d.", current, latest)
		res.NextStep = "Run the migrations: `docker compose run --rm migrate` (or `orchestrator -migrate-only`), then restart the orchestrator."
	case current > latest:
		res.Status = StatusWarn
		res.Detail = fmt.Sprintf("The database is at version %d, which is newer than this build (version %d).", current, latest)
		res.NextStep = "This happens after rolling the app back. Run the release the database was migrated by, or restore the backup taken before the upgrade."
	default:
		res.Status = StatusOK
		res.Detail = fmt.Sprintf("At version %d, up to date.", current)
	}
	return []Result{res}
}
