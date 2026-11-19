package twilio

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/doctor"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests moved from internal/doctor with the check (V4-31).

var adminScope = doctor.Scope{Admin: &models.User{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"}}

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

func TestWhatsAppCheck(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		assert.Equal(t, doctor.StatusInfo, one(t, WhatsAppCheck{}, adminScope).Status)
	})
	t.Run("sandbox sender", func(t *testing.T) {
		results := WhatsAppCheck{Configured: true, Sender: SandboxSender, StatusCallbackURL: "https://mypa.example/webhook/twilio/status"}.Run(context.Background(), adminScope)
		require.Len(t, results, 1)
		requireProblem(t, results[0], doctor.StatusWarn, "sandbox")
		assert.NotContains(t, results[0].Detail, "4155238886", "the sender is shown masked")
	})
	t.Run("own sender without delivery reports", func(t *testing.T) {
		results := WhatsAppCheck{Configured: true, Sender: "whatsapp:+12025550199"}.Run(context.Background(), adminScope)
		require.Len(t, results, 1)
		requireProblem(t, results[0], doctor.StatusWarn, "Delivery failures")
		assert.Contains(t, results[0].NextStep, "PUBLIC_URL")
	})
	t.Run("own sender with delivery reports", func(t *testing.T) {
		r := one(t, WhatsAppCheck{Configured: true, Sender: "whatsapp:+12025550199", StatusCallbackURL: "https://mypa.example/webhook/twilio/status"}, adminScope)
		assert.Equal(t, doctor.StatusOK, r.Status)
		assert.Contains(t, r.Detail, "…0199")
	})
	t.Run("sandbox without delivery reports reports both", func(t *testing.T) {
		results := WhatsAppCheck{Configured: true, Sender: SandboxSender}.Run(context.Background(), adminScope)
		require.Len(t, results, 2)
	})
}
