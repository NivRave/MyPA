package doctor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

var testNow = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return testNow }

var adminScope = Scope{Admin: &models.User{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"}}

// one runs a check and requires exactly one result.
func one(t *testing.T, c Check, scope Scope) Result {
	t.Helper()
	results := c.Run(context.Background(), scope)
	require.Len(t, results, 1, "%s", c.Name())
	return results[0]
}

// requireProblem asserts a result is a failure (or warning) that tells the admin what to do.
func requireProblem(t *testing.T, r Result, want Status, detailContains string) {
	t.Helper()
	assert.Equal(t, want, r.Status, "detail: %s", r.Detail)
	assert.Contains(t, r.Detail, detailContains)
	assert.NotEmpty(t, strings.TrimSpace(r.NextStep), "a problem must come with a next step")
}

// --- Telegram webhook -------------------------------------------------------------------------

func webhookCheck(info WebhookInfo, err error) TelegramWebhook {
	return TelegramWebhook{
		Configured: true,
		WantURL:    "https://mypa.example/webhook/telegram",
		Info:       func(context.Context) (WebhookInfo, error) { return info, err },
		Now:        fixedNow,
	}
}

func TestTelegramWebhook_Healthy(t *testing.T) {
	r := one(t, webhookCheck(WebhookInfo{URL: "https://mypa.example/webhook/telegram"}, nil), adminScope)
	assert.Equal(t, StatusOK, r.Status)
	assert.Empty(t, r.NextStep)
}

// V4-17 "done when": a broken webhook is detected. Each way it breaks is a failure with a next step.
func TestTelegramWebhook_Broken(t *testing.T) {
	t.Run("nothing registered", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookInfo{URL: ""}, nil), adminScope)
		requireProblem(t, r, StatusFail, "No webhook is registered")
	})
	t.Run("registered to another address", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookInfo{URL: "https://old-tunnel.example/webhook/telegram"}, nil), adminScope)
		requireProblem(t, r, StatusFail, "different address")
		assert.Contains(t, r.Detail, "old-tunnel.example")
	})
	t.Run("deliveries are failing", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookInfo{
			URL:          "https://mypa.example/webhook/telegram",
			Pending:      3,
			LastErrorAt:  testNow.Add(-12 * time.Minute),
			LastErrorMsg: "Connection refused",
		}, nil), adminScope)
		requireProblem(t, r, StatusFail, "Connection refused")
		assert.Contains(t, r.Detail, "12 minutes ago")
		assert.Contains(t, r.Detail, "3 updates are waiting")
	})
	t.Run("telegram cannot be asked", func(t *testing.T) {
		r := one(t, webhookCheck(WebhookInfo{}, errors.New("Post: dial tcp: i/o timeout")), adminScope)
		requireProblem(t, r, StatusFail, "Could not ask Telegram")
	})
}

func TestTelegramWebhook_OldErrorWithNothingPendingIsAWarning(t *testing.T) {
	r := one(t, webhookCheck(WebhookInfo{
		URL:          "https://mypa.example/webhook/telegram",
		LastErrorAt:  testNow.Add(-5 * time.Hour),
		LastErrorMsg: "Read timeout expired",
	}, nil), adminScope)
	requireProblem(t, r, StatusWarn, "Read timeout expired")
}

func TestTelegramWebhook_ErrorOlderThanADayIsHistory(t *testing.T) {
	r := one(t, webhookCheck(WebhookInfo{
		URL:          "https://mypa.example/webhook/telegram",
		LastErrorAt:  testNow.Add(-72 * time.Hour),
		LastErrorMsg: "Connection refused",
	}, nil), adminScope)
	assert.Equal(t, StatusOK, r.Status)
}

func TestTelegramWebhook_NoConfiguredAddressSkipsTheComparison(t *testing.T) {
	c := webhookCheck(WebhookInfo{URL: "https://anything.example/webhook/telegram"}, nil)
	c.WantURL = ""
	assert.Equal(t, StatusOK, one(t, c, adminScope).Status)
}

func TestTelegramWebhook_NotConfigured(t *testing.T) {
	c := TelegramWebhook{Configured: false, Info: func(context.Context) (WebhookInfo, error) {
		t.Fatal("Telegram must not be called when it is not configured")
		return WebhookInfo{}, nil
	}}
	assert.Equal(t, StatusInfo, one(t, c, adminScope).Status)
}

// --- Google accounts --------------------------------------------------------------------------

type googleFixture struct {
	members    map[string][]models.User // family group -> members
	tokens     map[string][]byte
	tokenErr   map[string]error
	refreshErr map[string]error // keyed by access token
	asked      []string         // family groups the check listed
}

func (g *googleFixture) check() GoogleAccounts {
	return GoogleAccounts{
		Configured: true,
		FamilyMembers: func(familyGroup string) ([]models.User, error) {
			g.asked = append(g.asked, familyGroup)
			return g.members[familyGroup], nil
		},
		Token: func(_ context.Context, userID string) ([]byte, error) {
			return g.tokens[userID], g.tokenErr[userID]
		},
		Refresh: func(_ context.Context, tok *oauth2.Token) error {
			return g.refreshErr[tok.AccessToken]
		},
	}
}

func tokenJSON(access, refresh string) []byte {
	return []byte(`{"access_token":"` + access + `","refresh_token":"` + refresh + `","token_type":"Bearer","expiry":"2026-11-19T08:00:00Z"}`)
}

func byCheck(results []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range results {
		out[r.Check] = r
	}
	return out
}

// V4-17 "done when": an expired Google token is detected, per user.
func TestGoogleAccounts_PerUserHealth(t *testing.T) {
	g := &googleFixture{
		members: map[string][]models.User{"Levi": {
			{PlatformID: "1001", Name: "Dana", FamilyGroup: "Levi"},
			{PlatformID: "1002", Name: "Omer", FamilyGroup: "Levi"},
			{PlatformID: "1003", Name: "Noa", FamilyGroup: "Levi"},
			{PlatformID: "+12025550104", Name: "Unknown", FamilyGroup: "Levi"},
			{PlatformID: "1005", Name: "Gil", FamilyGroup: "Levi"},
		}},
		tokens: map[string][]byte{
			"1001":         tokenJSON("dana-access", "dana-refresh"),
			"1002":         tokenJSON("omer-access", "omer-refresh"),
			"+12025550104": tokenJSON("anon-access", ""),
			"1005":         []byte("not json"),
		},
		refreshErr: map[string]error{"omer-access": errors.New(`oauth2: "invalid_grant" "Token has been expired or revoked."`)},
	}

	results := g.check().Run(context.Background(), adminScope)
	require.Len(t, results, 5, "one result per member")
	got := byCheck(results)

	assert.Equal(t, StatusOK, got["Google account: Dana"].Status)

	requireProblem(t, got["Google account: Omer"], StatusFail, "expired or was revoked")
	assert.Contains(t, got["Google account: Omer"].NextStep, "/connect")
	assert.Contains(t, got["Google account: Omer"].NextStep, "Omer")

	assert.Equal(t, StatusInfo, got["Google account: Noa"].Status, "never connected is a fact, not a failure")

	// A member without a name is shown by the tail of their ID, never by the full phone number.
	anon, ok := got["Google account: member …0104"]
	require.True(t, ok, "got checks: %v", results)
	requireProblem(t, anon, StatusFail, "cannot be renewed")
	for _, r := range results {
		assert.NotContains(t, r.Check+r.Detail+r.NextStep, "+12025550104", "the report must not print a phone number")
	}

	requireProblem(t, got["Google account: Gil"], StatusFail, "cannot be read")
}

// Tenant isolation: the check lists the admin's own family group and nobody else.
func TestGoogleAccounts_ScopedToTheAdminsFamily(t *testing.T) {
	g := &googleFixture{members: map[string][]models.User{
		"Levi":  {{PlatformID: "1001", Name: "Dana", FamilyGroup: "Levi"}},
		"Cohen": {{PlatformID: "2001", Name: "Stranger", FamilyGroup: "Cohen"}},
	}}
	results := g.check().Run(context.Background(), adminScope)

	assert.Equal(t, []string{"Levi"}, g.asked)
	for _, r := range results {
		assert.NotContains(t, r.Check, "Stranger")
	}
}

// An admin without a family group is their own tenant: only they are checked, and the lookup for
// an empty group (which would match every group-less user) is never made.
func TestGoogleAccounts_AdminWithoutFamilyIsCheckedAlone(t *testing.T) {
	g := &googleFixture{
		members: map[string][]models.User{"": {{PlatformID: "9", Name: "Other group-less user"}}},
		tokens:  map[string][]byte{"1001": tokenJSON("solo-access", "solo-refresh")},
	}
	solo := Scope{Admin: &models.User{PlatformID: "1001", Name: "Solo", Role: "admin"}}

	results := g.check().Run(context.Background(), solo)

	assert.Empty(t, g.asked)
	require.Len(t, results, 1)
	assert.Equal(t, "Google account: Solo", results[0].Check)
	assert.Equal(t, StatusOK, results[0].Status)
}

func TestGoogleAccounts_NotConfigured(t *testing.T) {
	c := GoogleAccounts{Configured: false}
	assert.Equal(t, StatusInfo, one(t, c, adminScope).Status)
}

// RefreshWith is the production Refresh: it must ask Google to renew the token even when the
// stored access token still looks valid, and report a refusal.
func TestRefreshWith_DetectsARevokedToken(t *testing.T) {
	var refreshRequests int
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
		refreshRequests++
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("refresh_token") == "revoked" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer google.Close()

	cfg := &oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: google.URL, AuthStyle: oauth2.AuthStyleInParams}}
	refresh := RefreshWith(cfg.TokenSource)
	stillValid := time.Now().Add(30 * time.Minute)

	err := refresh(context.Background(), &oauth2.Token{AccessToken: "old", RefreshToken: "revoked", Expiry: stillValid})
	require.Error(t, err, "a revoked refresh token must be detected even while the access token is valid")

	original := &oauth2.Token{AccessToken: "old", RefreshToken: "good", Expiry: stillValid}
	require.NoError(t, refresh(context.Background(), original))
	assert.Equal(t, 2, refreshRequests, "every check asks Google")
	assert.Equal(t, stillValid, original.Expiry, "the caller's token is not modified")
}

// --- WhatsApp ---------------------------------------------------------------------------------

func TestWhatsApp(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		assert.Equal(t, StatusInfo, one(t, WhatsApp{}, adminScope).Status)
	})
	t.Run("sandbox sender", func(t *testing.T) {
		results := WhatsApp{Configured: true, Sender: TwilioSandboxSender, StatusCallbackURL: "https://mypa.example/webhook/twilio/status"}.Run(context.Background(), adminScope)
		require.Len(t, results, 1)
		requireProblem(t, results[0], StatusWarn, "sandbox")
		assert.NotContains(t, results[0].Detail, "4155238886", "the sender is shown masked")
	})
	t.Run("own sender without delivery reports", func(t *testing.T) {
		results := WhatsApp{Configured: true, Sender: "whatsapp:+12025550199"}.Run(context.Background(), adminScope)
		require.Len(t, results, 1)
		requireProblem(t, results[0], StatusWarn, "Delivery failures")
		assert.Contains(t, results[0].NextStep, "TWILIO_STATUS_CALLBACK_URL")
	})
	t.Run("own sender with delivery reports", func(t *testing.T) {
		r := one(t, WhatsApp{Configured: true, Sender: "whatsapp:+12025550199", StatusCallbackURL: "https://mypa.example/webhook/twilio/status"}, adminScope)
		assert.Equal(t, StatusOK, r.Status)
		assert.Contains(t, r.Detail, "…0199")
	})
	t.Run("sandbox without delivery reports reports both", func(t *testing.T) {
		results := WhatsApp{Configured: true, Sender: TwilioSandboxSender}.Run(context.Background(), adminScope)
		require.Len(t, results, 2)
	})
}

// --- Queues -----------------------------------------------------------------------------------

func queueCheck(depths map[string]int, errs map[string]error) Queues {
	return Queues{
		Names:       []string{"inbound.messages", "audit.events"},
		BacklogWarn: 20,
		Depth: func(_ context.Context, queue string) (int, error) {
			return depths[queue], errs[queue]
		},
	}
}

func TestQueues(t *testing.T) {
	t.Run("empty queues are healthy", func(t *testing.T) {
		results := queueCheck(nil, nil).Run(context.Background(), adminScope)
		require.Len(t, results, 2, "one result per queue")
		for _, r := range results {
			assert.Equal(t, StatusOK, r.Status)
		}
	})
	t.Run("failed messages", func(t *testing.T) {
		got := byCheck(queueCheck(map[string]int{"inbound.messages.dlq": 2}, nil).Run(context.Background(), adminScope))
		requireProblem(t, got["Queue inbound.messages"], StatusFail, "2 messages failed")
		assert.Equal(t, StatusOK, got["Queue audit.events"].Status)
	})
	t.Run("backlog", func(t *testing.T) {
		got := byCheck(queueCheck(map[string]int{"audit.events": 45}, nil).Run(context.Background(), adminScope))
		requireProblem(t, got["Queue audit.events"], StatusWarn, "45 messages are waiting")
	})
	t.Run("a short queue is normal", func(t *testing.T) {
		got := byCheck(queueCheck(map[string]int{"inbound.messages": 3}, nil).Run(context.Background(), adminScope))
		assert.Equal(t, StatusOK, got["Queue inbound.messages"].Status)
	})
	t.Run("broker unreachable", func(t *testing.T) {
		got := byCheck(queueCheck(nil, map[string]error{"inbound.messages": errors.New("dial tcp: connection refused")}).Run(context.Background(), adminScope))
		requireProblem(t, got["Queue inbound.messages"], StatusFail, "Could not read")
	})
}

// --- Backups ----------------------------------------------------------------------------------

func backupCheck(name string, takenAt time.Time, err error) Backups {
	return Backups{
		MaxAge: 26 * time.Hour,
		Now:    fixedNow,
		Latest: func() (string, time.Time, error) { return name, takenAt, err },
	}
}

func TestBackups_Fresh(t *testing.T) {
	r := one(t, backupCheck("backup_2026-11-19_02-00-00.dump.age", testNow.Add(-7*time.Hour), nil), adminScope)
	assert.Equal(t, StatusOK, r.Status)
	assert.Contains(t, r.Detail, "7 hours ago")
	assert.Contains(t, r.Detail, "this machine only", "OP-11 stays visible")
}

// V4-17 "done when": a stale backup is detected.
func TestBackups_Stale(t *testing.T) {
	r := one(t, backupCheck("backup_2026-11-16_02-00-00.dump.age", testNow.Add(-79*time.Hour), nil), adminScope)
	requireProblem(t, r, StatusFail, "3 days ago")
	assert.Contains(t, r.NextStep, "/backup")
}

func TestBackups_JustOverTheLimit(t *testing.T) {
	assert.Equal(t, StatusOK, one(t, backupCheck("b.dump.age", testNow.Add(-26*time.Hour), nil), adminScope).Status)
	assert.Equal(t, StatusFail, one(t, backupCheck("b.dump.age", testNow.Add(-26*time.Hour-time.Minute), nil), adminScope).Status)
}

func TestBackups_None(t *testing.T) {
	r := one(t, backupCheck("", time.Time{}, errors.New("no backup files found in /backups")), adminScope)
	requireProblem(t, r, StatusFail, "No backup was found")
}

func TestBackups_Unencrypted(t *testing.T) {
	r := one(t, backupCheck("backup_2026-11-19_02-00-00.dump", testNow.Add(-7*time.Hour), nil), adminScope)
	requireProblem(t, r, StatusWarn, "not encrypted")
}

// --- Schema -----------------------------------------------------------------------------------

func schemaCheck(current, latest int64, err error) Schema {
	return Schema{Version: func(context.Context) (int64, int64, error) { return current, latest, err }}
}

func TestSchema(t *testing.T) {
	ok := one(t, schemaCheck(4, 4, nil), adminScope)
	assert.Equal(t, StatusOK, ok.Status)
	assert.Contains(t, ok.Detail, "version 4")

	requireProblem(t, one(t, schemaCheck(3, 4, nil), adminScope), StatusFail, "version 3")
	requireProblem(t, one(t, schemaCheck(5, 4, nil), adminScope), StatusWarn, "newer")
	requireProblem(t, one(t, schemaCheck(0, 0, errors.New("connection refused")), adminScope), StatusFail, "Could not read")
}
