package twilio

import (
	"context"
	"fmt"
	"strings"

	"github.com/nivik/mypa/internal/doctor"
)

// SandboxSender is the shared number of Twilio's WhatsApp sandbox.
const SandboxSender = "whatsapp:+14155238886"

// WhatsAppCheck is the /doctor check (V4-17) of the WhatsApp adapter, moved from internal/doctor
// with the channel layer (V4-31). It reports how WhatsApp is set up. Until the mode setting of
// V4-41 exists it reads the configuration only: whether the sender is Twilio's shared sandbox
// number, and whether delivery reports are switched on. It does not ask Twilio about the sender.
type WhatsAppCheck struct {
	Configured        bool   // a Twilio account is set
	Sender            string // TWILIO_FROM_NUMBER
	StatusCallbackURL string // TWILIO_STATUS_CALLBACK_URL
}

func (WhatsAppCheck) Name() string { return "WhatsApp" }

func (c WhatsAppCheck) Run(context.Context, doctor.Scope) []doctor.Result {
	if !c.Configured {
		return []doctor.Result{{Check: c.Name(), Status: doctor.StatusInfo, Detail: "WhatsApp is not configured."}}
	}
	var results []doctor.Result
	if strings.EqualFold(strings.TrimSpace(c.Sender), SandboxSender) {
		results = append(results, doctor.Result{
			Check:  "WhatsApp sender",
			Status: doctor.StatusWarn,
			Detail: "WhatsApp runs on the Twilio sandbox. A person who has not written for 72 hours drops out of it, " +
				"and replies and reminders to them then fail (Twilio error 63015).",
			NextStep: "When someone stops getting answers, have them send the sandbox join message again. " +
				"A registered sender removes the limit (planned as V4-41).",
		})
	}
	if strings.TrimSpace(c.StatusCallbackURL) == "" {
		results = append(results, doctor.Result{
			Check:    "WhatsApp delivery reports",
			Status:   doctor.StatusWarn,
			Detail:   "Delivery failures are not reported back, so a message Twilio accepted but could not deliver goes unnoticed.",
			NextStep: "Set `TWILIO_STATUS_CALLBACK_URL` to the public address of `/webhook/twilio/status` and restart the app.",
		})
	}
	if len(results) == 0 {
		results = append(results, doctor.Result{
			Check:  c.Name(),
			Status: doctor.StatusOK,
			Detail: fmt.Sprintf("Configured with sender %s; delivery reports are on.", doctor.MaskTail(c.Sender)),
		})
	}
	return results
}
