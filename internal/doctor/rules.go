package doctor

import (
	"context"
	"fmt"
	"strings"
)

// --- Automation rules (BL-63) -----------------------------------------------------------------

// IdleRules counts the automation rules of the admin's household whose action does nothing when
// they fire: neither a webhook, nor a notify: message, nor a prompt: on a schedule. Before BL-63 a
// rule created with a plain instruction was stored that way; such rules are not rewritten, so the
// owner can see and fix them.
type IdleRules struct {
	// Count returns, for one household, how many rules have such an action.
	Count func(ctx context.Context, householdID string) (int64, error)
}

func (IdleRules) Name() string { return "Automation rules" }

func (c IdleRules) Run(ctx context.Context, scope Scope) []Result {
	res := Result{Check: c.Name()}
	if c.Count == nil || scope.Admin == nil || strings.TrimSpace(scope.Admin.HouseholdID) == "" {
		res.Status = StatusInfo
		res.Detail = "Not checked: no household to count for."
		return []Result{res}
	}
	n, err := c.Count(ctx, scope.Admin.HouseholdID)
	switch {
	case err != nil:
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("Could not count the automation rules: %s.", reason(err))
		res.NextStep = "Check that Postgres is running and that `DATABASE_URL` is right."
	case n > 0:
		res.Status = StatusWarn
		res.Detail = fmt.Sprintf("%d automation rule(s) do nothing when they fire: their action is not a webhook, a `notify:` message or, on a schedule, a `prompt:`.", n)
		res.NextStep = "List them with \"list my automation rules\" and delete or recreate each one: on a schedule, an instruction is now stored as `prompt: ...` and runs as a turn; a rule triggered by an event can call a webhook or send `notify: ...`."
	default:
		res.Status = StatusOK
		res.Detail = "Every automation rule does something when it fires."
	}
	return []Result{res}
}
