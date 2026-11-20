package twilio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests moved from internal/doctor with the check (V4-31); V4-41 added the modes.

var adminScope = doctor.Scope{Admin: &models.User{PlatformID: "1001", Name: "Dana", Role: "admin", HouseholdID: "hh-levi", FamilyGroup: "Levi"}}

var checkNow = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

const callback = "https://mypa.example/webhook/twilio/status"

// one runs a check and requires exactly one result.
func one(t *testing.T, c doctor.Check, scope doctor.Scope) doctor.Result {
	t.Helper()
	results := c.Run(context.Background(), scope)
	require.Len(t, results, 1, "%s", c.Name())
	return results[0]
}

// requireProblem asserts a result is a failure (or warning) that tells the admin what to do.
func requireProblem(t *testing.T, r doctor.Result, want doctor.Status, detailContains string) {
	t.Helper()
	assert.Equal(t, want, r.Status, "detail: %s", r.Detail)
	assert.Contains(t, r.Detail, detailContains)
	assert.NotEmpty(t, strings.TrimSpace(r.NextStep), "a problem must come with a next step")
}

// byCheck returns the result with the given check name.
func byCheck(t *testing.T, results []doctor.Result, name string) doctor.Result {
	t.Helper()
	for _, r := range results {
		if r.Check == name {
			return r
		}
	}
	t.Fatalf("no result %q in %+v", name, results)
	return doctor.Result{}
}

func hasCheck(results []doctor.Result, name string) bool {
	for _, r := range results {
		if r.Check == name {
			return true
		}
	}
	return false
}

func sandboxCheck() WhatsAppCheck {
	return WhatsAppCheck{
		Configured: true, Mode: ModeSandbox, Sender: SandboxSender, SandboxJoin: "join plenty-tiger",
		StatusCallbackURL: callback, Now: func() time.Time { return checkNow },
		Activity: func(context.Context, string) ([]channel.AccountActivity, error) { return nil, nil },
	}
}

func senderCheck(approvals map[string]Approval) WhatsAppCheck {
	return WhatsAppCheck{
		Configured: true, Mode: ModeSender, Sender: "whatsapp:+12025550199", StatusCallbackURL: callback,
		TemplateSIDs: map[string]string{TemplateReminder: "HXrem", TemplateBriefing: "HXbri"},
		Approval: func(_ context.Context, sid string) (Approval, error) {
			a, ok := approvals[sid]
			if !ok {
				return Approval{}, errors.New("connection refused")
			}
			return a, nil
		},
	}
}

func TestWhatsAppCheck_NotConfigured(t *testing.T) {
	assert.Equal(t, doctor.StatusInfo, one(t, WhatsAppCheck{}, adminScope).Status)
}

// TestWhatsAppCheck_ReportsTheMode: /doctor says which mode runs, in both (V4-41).
func TestWhatsAppCheck_ReportsTheMode(t *testing.T) {
	sb := byCheck(t, sandboxCheck().Run(context.Background(), adminScope), "WhatsApp mode")
	assert.Equal(t, doctor.StatusInfo, sb.Status)
	assert.Contains(t, sb.Detail, "Sandbox")
	assert.Contains(t, sb.Detail, "72 hours")
	assert.NotContains(t, sb.Detail, "4155238886", "the sender is shown masked")

	se := byCheck(t, senderCheck(nil).Run(context.Background(), adminScope), "WhatsApp mode")
	assert.Equal(t, doctor.StatusInfo, se.Status)
	assert.Contains(t, se.Detail, "Registered sender")
	assert.Contains(t, se.Detail, "…0199")
}

func TestWhatsAppCheck_Sandbox(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		results := sandboxCheck().Run(context.Background(), adminScope)
		for _, r := range results {
			assert.Less(t, r.Status, doctor.StatusWarn, "%s: %s", r.Check, r.Detail)
		}
		assert.False(t, hasCheck(results, "WhatsApp template mypa_reminder"), "a sandbox has no templates of MyPA's to check")
	})
	t.Run("a lapsed membership is detected and explained", func(t *testing.T) {
		c := sandboxCheck()
		var asked string
		c.Activity = func(_ context.Context, householdID string) ([]channel.AccountActivity, error) {
			asked = householdID
			return []channel.AccountActivity{
				{Person: "Dana", LastInboundAt: checkNow.Add(-2 * time.Hour)},
				{Person: "Omer", LastInboundAt: checkNow.Add(-4 * 24 * time.Hour)},
				{Person: "Noa"}, // never wrote: nothing to say
			}, nil
		}
		results := c.Run(context.Background(), adminScope)
		assert.Equal(t, "hh-levi", asked, "only the admin's own household")
		r := byCheck(t, results, "WhatsApp sandbox: Omer")
		requireProblem(t, r, doctor.StatusWarn, "4 days ago")
		assert.Contains(t, r.Detail, "63015")
		assert.Contains(t, r.NextStep, "join plenty-tiger", "the next step is the exact message to send")
		assert.Contains(t, r.NextStep, "+14155238886", "and where to send it: Twilio's public sandbox number")
		assert.False(t, hasCheck(results, "WhatsApp sandbox: Dana"))
		assert.False(t, hasCheck(results, "WhatsApp sandbox: Noa"))
	})
	t.Run("everyone current", func(t *testing.T) {
		c := sandboxCheck()
		c.Activity = func(context.Context, string) ([]channel.AccountActivity, error) {
			return []channel.AccountActivity{{Person: "Dana", LastInboundAt: checkNow.Add(-71 * time.Hour)}}, nil
		}
		assert.Equal(t, doctor.StatusOK, byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp sandbox members").Status)
	})
	t.Run("activity cannot be read", func(t *testing.T) {
		c := sandboxCheck()
		c.Activity = func(context.Context, string) ([]channel.AccountActivity, error) { return nil, errors.New("db down") }
		requireProblem(t, byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp sandbox members"), doctor.StatusWarn, "could not")
	})
	t.Run("no join code", func(t *testing.T) {
		c := sandboxCheck()
		c.SandboxJoin = ""
		r := byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp join message")
		requireProblem(t, r, doctor.StatusWarn, "invitations")
		assert.Contains(t, r.NextStep, "TWILIO_SANDBOX_JOIN_CODE")
	})
	t.Run("sandbox mode on another number", func(t *testing.T) {
		c := sandboxCheck()
		c.Sender = "whatsapp:+12025550199"
		r := byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp mode")
		assert.Equal(t, doctor.StatusWarn, r.Status)
		assert.Contains(t, r.NextStep, "TWILIO_WHATSAPP_MODE=sender")
	})
}

func TestWhatsAppCheck_SenderTemplates(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		results := senderCheck(map[string]Approval{"HXrem": {Status: "approved"}, "HXbri": {Status: "approved"}}).Run(context.Background(), adminScope)
		assert.Equal(t, doctor.StatusOK, byCheck(t, results, "WhatsApp template mypa_reminder").Status)
		assert.Equal(t, doctor.StatusOK, byCheck(t, results, "WhatsApp template mypa_briefing").Status)
		assert.False(t, hasCheck(results, "WhatsApp sandbox members"), "a registered sender has no membership to lapse")
	})
	t.Run("waiting, rejected, unreachable", func(t *testing.T) {
		c := senderCheck(map[string]Approval{"HXrem": {Status: "received"}})
		c.TemplateSIDs[TemplateBriefing] = "HXbri"
		results := c.Run(context.Background(), adminScope)
		requireProblem(t, byCheck(t, results, "WhatsApp template mypa_reminder"), doctor.StatusWarn, "Waiting")
		requireProblem(t, byCheck(t, results, "WhatsApp template mypa_briefing"), doctor.StatusWarn, "connection refused")

		rejected := senderCheck(map[string]Approval{"HXrem": {Status: "rejected", Reason: "Variables at the end"}, "HXbri": {Status: "unsubmitted"}}).Run(context.Background(), adminScope)
		requireProblem(t, byCheck(t, rejected, "WhatsApp template mypa_reminder"), doctor.StatusFail, "Variables at the end")
		requireProblem(t, byCheck(t, rejected, "WhatsApp template mypa_briefing"), doctor.StatusWarn, "not been submitted")
	})
	t.Run("not set up", func(t *testing.T) {
		c := senderCheck(nil)
		c.TemplateSIDs = nil
		r := byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp template mypa_reminder")
		requireProblem(t, r, doctor.StatusWarn, "24 hours")
		assert.Contains(t, r.NextStep, "mypa channel-setup whatsapp")
		assert.Contains(t, r.NextStep, "TWILIO_TEMPLATE_REMINDER_SID")
	})
}

func TestWhatsAppCheck_DeliveryReports(t *testing.T) {
	c := senderCheck(map[string]Approval{"HXrem": {Status: "approved"}, "HXbri": {Status: "approved"}})
	c.StatusCallbackURL = ""
	r := byCheck(t, c.Run(context.Background(), adminScope), "WhatsApp delivery reports")
	requireProblem(t, r, doctor.StatusWarn, "Delivery failures")
	assert.Contains(t, r.NextStep, "PUBLIC_URL")

	on := byCheck(t, sandboxCheck().Run(context.Background(), adminScope), "WhatsApp delivery reports")
	assert.Equal(t, doctor.StatusOK, on.Status)
}

// TestAdapter_HealthCheckFollowsTheMode: the adapter's own check reads its settings and the
// activity source it is given.
func TestAdapter_HealthCheckFollowsTheMode(t *testing.T) {
	src := func(_ context.Context, householdID, channelName string) ([]channel.AccountActivity, error) {
		assert.Equal(t, Name, channelName)
		return nil, nil
	}
	sb := NewAdapter(Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: SandboxSender}).HealthCheck(src).(WhatsAppCheck)
	assert.Equal(t, ModeSandbox, sb.Mode)
	require.NotNil(t, sb.Activity)
	_, _ = sb.Activity(context.Background(), "hh")

	se := NewAdapter(Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: "whatsapp:+12025550199", TemplateSIDs: map[string]string{TemplateReminder: "HXa"}}).HealthCheck(nil).(WhatsAppCheck)
	assert.Equal(t, ModeSender, se.Mode)
	assert.Equal(t, "HXa", se.TemplateSIDs[TemplateReminder])
	assert.NotNil(t, se.Approval)
}
