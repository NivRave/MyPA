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

// GoogleAccounts checks, for every member of the admin's household, whether their Google
// sign-in still works: a stored token exists, can be read, and Google agrees to renew it.
type GoogleAccounts struct {
	Configured bool // a Google OAuth client is set up
	// HouseholdMembers lists the users of one household, by its ID (V4-30). It is never called
	// with an empty ID.
	HouseholdMembers func(householdID string) ([]models.User, error)
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

	// Tenant scope: the admin's own household. Every user has one since V4-30; an admin row
	// without one is checked alone rather than listed against an empty key.
	members := []models.User{*scope.Admin}
	if household := strings.TrimSpace(scope.Admin.HouseholdID); household != "" {
		listed, err := c.HouseholdMembers(household)
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
			res.NextStep = "Check that the service reading this queue is running and not stuck (the app for inbound.messages, audit-worker for audit.events)."
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
		res.NextStep = "Send /backup to take one now, then look for \"Scheduled backup\" in the app log around 02:00 to see why the nightly run failed."
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

// --- Allowed users ----------------------------------------------------------------------------

// AllowedUsers says whether the ALLOWED_USERS setting still does anything. Since V4-30 it seeds a
// fresh install only: once a household exists it is ignored (ADR 0002 Decision 6), and an owner
// who edits it and restarts would otherwise wonder why nothing changed.
type AllowedUsers struct {
	// Set reports whether ALLOWED_USERS has any entry.
	Set bool
	// Households counts the install's households.
	Households func(ctx context.Context) (int64, error)
}

func (AllowedUsers) Name() string { return "Allowed users" }

func (c AllowedUsers) Run(ctx context.Context, _ Scope) []Result {
	if !c.Set {
		return nil
	}
	res := Result{Check: c.Name()}
	n, err := c.Households(ctx)
	switch {
	case err != nil:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not count the households: %s.", reason(err))
		res.NextStep = "Check that Postgres is running and that `DATABASE_URL` is right."
	case n == 0:
		res.Status = StatusWarn
		res.Detail = "`ALLOWED_USERS` is set but no household exists, so nobody can use MyPA."
		res.NextStep = "Restart the app: on a start with no household it creates the households and users from `ALLOWED_USERS`."
	default:
		res.Status = StatusInfo
		res.Detail = "`ALLOWED_USERS` is set but ignored: this install already has its household, so the database decides who may use MyPA."
		res.NextStep = "Add people with a pairing code from the dashboard. You can remove `ALLOWED_USERS` from the settings file."
	}
	return []Result{res}
}

// --- Owners -----------------------------------------------------------------------------------

// Owners says whether every household has an owner (V4-35). The upgrade makes every admin an
// owner, and gives a household that had no admin no owner (ADR 0002 Decision 2, Migration C):
// nobody can then manage its settings or members until the operator assigns one.
type Owners struct {
	// Ownerless counts the households that have members but no owner.
	Ownerless func(ctx context.Context) (int64, error)
}

func (Owners) Name() string { return "Household owners" }

func (c Owners) Run(ctx context.Context, _ Scope) []Result {
	res := Result{Check: c.Name()}
	n, err := c.Ownerless(ctx)
	switch {
	case err != nil:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not check the households' owners: %s.", reason(err))
		res.NextStep = "Check that Postgres is running and that `DATABASE_URL` is right."
	case n > 0:
		res.Status = StatusWarn
		res.Detail = fmt.Sprintf("%d household(s) have members but no owner, so nobody can change their settings or add members.", n)
		res.NextStep = "On the machine MyPA runs on, make one member the owner: `docker compose run --rm app access grant owner <the ID they write from>`."
	default:
		res.Status = StatusOK
		res.Detail = "Every household has an owner."
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
		res.NextStep = "Run the migrations: `docker compose run --rm migrate` (or `mypa -migrate-only`), then restart the app."
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
