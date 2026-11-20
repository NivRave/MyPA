package twilio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/doctor"
)

// SandboxSender is the shared number of Twilio's WhatsApp sandbox.
const SandboxSender = "whatsapp:+14155238886"

// SandboxMembership is how long a sandbox membership lasts after the person last wrote: Twilio
// removes a participant who has not written for 72 hours, and messages to them then fail with
// error 63015.
const SandboxMembership = 72 * time.Hour

// Approval is a template's WhatsApp approval as Twilio reports it.
type Approval struct {
	// Status is Twilio's value: "approved", "received" or "pending" while WhatsApp reviews it,
	// "rejected", "paused", "disabled", or "unsubmitted".
	Status string
	Reason string // why WhatsApp rejected it
}

// WhatsAppCheck is the /doctor check (V4-17) of the WhatsApp adapter, moved from internal/doctor
// with the channel layer (V4-31). Since V4-41 it reports the mode and the health of that mode: in
// a sandbox, the join message and whose membership has lapsed; with a registered sender, the
// approval of MyPA's templates, asked of Twilio. Both report whether delivery reports are on.
type WhatsAppCheck struct {
	Configured        bool   // a Twilio account is set
	Mode              Mode   // the mode in use
	Sender            string // TWILIO_FROM_NUMBER
	SandboxJoin       string // "join <code>", or empty when not set
	StatusCallbackURL string // derived from PUBLIC_URL (V4-40)
	// TemplateSIDs and Approval: sender mode. Approval asks Twilio about one template.
	TemplateSIDs map[string]string
	Approval     func(ctx context.Context, sid string) (Approval, error)
	// Activity lists the household's WhatsApp accounts and when each last wrote (sandbox mode).
	// Nil: not checked.
	Activity func(ctx context.Context, householdID string) ([]channel.AccountActivity, error)
	Now      func() time.Time
}

func (WhatsAppCheck) Name() string { return "WhatsApp" }

func (c WhatsAppCheck) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c WhatsAppCheck) Run(ctx context.Context, scope doctor.Scope) []doctor.Result {
	if !c.Configured {
		return []doctor.Result{{Check: c.Name(), Status: doctor.StatusInfo, Detail: "WhatsApp is not configured."}}
	}
	var results []doctor.Result
	if c.Mode == ModeSender {
		results = append(results, doctor.Result{
			Check:  "WhatsApp mode",
			Status: doctor.StatusInfo,
			Detail: fmt.Sprintf("Registered sender %s. Messages outside the 24-hour window go out with MyPA's approved templates.", doctor.MaskTail(c.Sender)),
		})
		results = append(results, c.templates(ctx)...)
	} else {
		results = append(results, c.sandbox(ctx, scope)...)
	}
	if strings.TrimSpace(c.StatusCallbackURL) == "" {
		results = append(results, doctor.Result{
			Check:    "WhatsApp delivery reports",
			Status:   doctor.StatusWarn,
			Detail:   "Delivery failures are not reported back, so a message Twilio accepted but could not deliver goes unnoticed.",
			NextStep: "Set `PUBLIC_URL` to this install's public address and restart the app: delivery reports go to `/webhook/twilio/status` there.",
		})
	} else {
		results = append(results, doctor.Result{
			Check:  "WhatsApp delivery reports",
			Status: doctor.StatusOK,
			Detail: "On: a message Twilio could not deliver is logged with its reason.",
		})
	}
	return results
}

// sandbox reports the sandbox mode: its limits, the join message invitations show, and every
// member of the admin's household whose membership has lapsed.
func (c WhatsAppCheck) sandbox(ctx context.Context, scope doctor.Scope) []doctor.Result {
	mode := doctor.Result{
		Check:  "WhatsApp mode",
		Status: doctor.StatusInfo,
		Detail: fmt.Sprintf("Sandbox, on Twilio's shared number %s. A person joins it with the join message and drops out 72 hours after they last wrote; "+
			"MyPA has no templates here, so a reminder outside the 24-hour window cannot go out on WhatsApp.", doctor.MaskTail(c.Sender)),
	}
	if !isSandboxNumber(c.Sender) {
		mode.Status = doctor.StatusWarn
		mode.Detail = fmt.Sprintf("Sandbox mode is set, but the sender %s is not Twilio's sandbox number.", doctor.MaskTail(c.Sender))
		mode.NextStep = "For a registered sender set `TWILIO_WHATSAPP_MODE=sender`; for the sandbox set `TWILIO_FROM_NUMBER` to its number. Then restart the app."
	}
	results := []doctor.Result{mode}
	if c.SandboxJoin == "" {
		results = append(results, doctor.Result{
			Check:  "WhatsApp join message",
			Status: doctor.StatusWarn,
			Detail: "The sandbox's join code is not set, so invitations cannot say what a new person sends before their first message.",
			NextStep: "Copy the code from the Twilio Console (Messaging → Try it out → Send a WhatsApp message) into " +
				"`TWILIO_SANDBOX_JOIN_CODE` and restart the app.",
		})
	}
	if c.Activity == nil || scope.Admin == nil {
		return results
	}
	accounts, err := c.Activity(ctx, scope.Admin.HouseholdID)
	if err != nil {
		return append(results, doctor.Result{
			Check:    "WhatsApp sandbox members",
			Status:   doctor.StatusWarn,
			Detail:   fmt.Sprintf("I could not read when members last wrote: %s.", doctor.Reason(err)),
			NextStep: "Check the database connection, then run /doctor again.",
		})
	}
	now := c.now()
	lapsed := 0
	for _, a := range accounts {
		if a.LastInboundAt.IsZero() || now.Sub(a.LastInboundAt) < SandboxMembership {
			continue
		}
		lapsed++
		results = append(results, doctor.Result{
			Check:  "WhatsApp sandbox: " + a.Person,
			Status: doctor.StatusWarn,
			Detail: fmt.Sprintf("%s last wrote on WhatsApp %s, so their sandbox membership has lapsed: replies and reminders to them there fail (Twilio error 63015) until they join again.",
				a.Person, doctor.Ago(now.Sub(a.LastInboundAt))),
			NextStep: fmt.Sprintf("Ask %s to send %s on WhatsApp to %s, Twilio's sandbox number, and then write to MyPA.",
				a.Person, c.joinText(), strings.TrimPrefix(SandboxSender, "whatsapp:")),
		})
	}
	if lapsed == 0 && len(accounts) > 0 {
		results = append(results, doctor.Result{
			Check:  "WhatsApp sandbox members",
			Status: doctor.StatusOK,
			Detail: "Everyone on WhatsApp wrote within the last 72 hours.",
		})
	}
	return results
}

// joinText is the join message as a next step shows it.
func (c WhatsAppCheck) joinText() string {
	if c.SandboxJoin == "" {
		return "the sandbox's join message (Twilio Console → Messaging → Try it out)"
	}
	return "`" + c.SandboxJoin + "`"
}

// templates reports, for each of MyPA's templates, whether it is set and what WhatsApp decided.
func (c WhatsAppCheck) templates(ctx context.Context) []doctor.Result {
	var results []doctor.Result
	for _, tp := range Templates() {
		r := doctor.Result{Check: "WhatsApp template " + tp.Name}
		sid := c.TemplateSIDs[tp.Key]
		var approval Approval
		var err error
		switch {
		case sid == "":
			r.Status = doctor.StatusWarn
			r.Detail = "Not set up. " + strings.TrimSuffix(tp.Why, ".") + " cannot go out on WhatsApp without it: outside the 24-hour window WhatsApp takes only approved templates."
			r.NextStep = fmt.Sprintf("Run `mypa channel-setup whatsapp` and follow its steps, then set `%s` and restart the app.", templateEnvKeys[tp.Key])
			results = append(results, r)
			continue
		case c.Approval == nil:
			continue
		}
		approval, err = c.Approval(ctx, sid)
		switch {
		case err != nil:
			r.Status = doctor.StatusWarn
			r.Detail = fmt.Sprintf("I could not ask Twilio about it: %s.", doctor.Reason(err))
			r.NextStep = fmt.Sprintf("Check the network and that `%s` is the template's Content SID, then run /doctor again.", templateEnvKeys[tp.Key])
		case approval.Status == "approved":
			r.Status = doctor.StatusOK
			r.Detail = "Approved by WhatsApp."
		case approval.Status == "received" || approval.Status == "pending":
			r.Status = doctor.StatusWarn
			r.Detail = "Waiting for WhatsApp's approval (" + approval.Status + ")."
			r.NextStep = "Approval usually takes from a few minutes to a day. Run /doctor again later."
		case approval.Status == "rejected":
			r.Status = doctor.StatusFail
			r.Detail = "WhatsApp rejected it: " + strings.TrimSuffix(strings.TrimSpace(approval.Reason), ".") + "."
			r.NextStep = "Change the wording in the Twilio Console (Messaging → Content Template Builder) as the reason says, submit it again, and set its new Content SID."
		case approval.Status == "" || approval.Status == "unsubmitted":
			r.Status = doctor.StatusWarn
			r.Detail = "It exists in Twilio but has not been submitted for WhatsApp's approval."
			r.NextStep = "Run `mypa channel-setup whatsapp --submit`, or submit it in the Twilio Console."
		default: // paused, disabled
			r.Status = doctor.StatusFail
			r.Detail = "WhatsApp has " + approval.Status + " it, so it cannot be sent."
			r.NextStep = "Open it in the Twilio Console (Messaging → Content Template Builder) to see why, then fix or replace it."
		}
		results = append(results, r)
	}
	return results
}
