package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- Proactive delivery (V4-33) ---------------------------------------------------------------

// HeldMessages counts the messages the assistant started for the admin's household that no
// channel would take yet: they wait for their person to write (ADR 0003 §4: "/doctor counts what
// is held"), and those that expired undelivered in the last week.
type HeldMessages struct {
	// Counts returns, for one household, the messages still waiting and those expired since.
	Counts func(ctx context.Context, householdID string, since time.Time) (waiting, expired int64, err error)
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// heldWindow is how far back expired messages are counted.
const heldWindow = 7 * 24 * time.Hour

func (HeldMessages) Name() string { return "Messages waiting" }

func (c HeldMessages) Run(ctx context.Context, scope Scope) []Result {
	res := Result{Check: c.Name()}
	if c.Counts == nil || scope.Admin == nil || strings.TrimSpace(scope.Admin.HouseholdID) == "" {
		res.Status = StatusInfo
		res.Detail = "Not checked: no household to count for."
		return []Result{res}
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	waiting, expired, err := c.Counts(ctx, scope.Admin.HouseholdID, now().Add(-heldWindow))
	switch {
	case err != nil:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not count the messages waiting for delivery: %s.", reason(err))
		res.NextStep = "Check that Postgres is running and that `DATABASE_URL` is right."
	case expired > 0:
		res.Status = StatusWarn
		res.Detail = fmt.Sprintf("%d message(s) wait for their person to write; %d expired undelivered in the last 7 days.", waiting, expired)
		res.NextStep = "A reminder expires after 7 days and a briefing at the end of its day when no channel would take it. " +
			"Ask the people concerned to write to MyPA once on each channel they use, or to link another channel with /link."
	case waiting > 0:
		res.Status = StatusInfo
		res.Detail = fmt.Sprintf("%d message(s) wait for their person to write; each goes out, marked late, when they do.", waiting)
	default:
		res.Status = StatusOK
		res.Detail = "No message is waiting for delivery."
	}
	return []Result{res}
}
