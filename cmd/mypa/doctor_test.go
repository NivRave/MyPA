package main

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// telegramAPI answers getWebhookInfo as the Bot API would; anything else fails the test.
type telegramAPI struct {
	t           *testing.T
	webhookInfo string // the JSON "result" object
}

func (a telegramAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.telegram.org" || !strings.HasSuffix(req.URL.Path, "/getWebhookInfo") {
		a.t.Errorf("unexpected request to %s%s", req.URL.Host, req.URL.Path)
		return nil, fmt.Errorf("unexpected request")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":` + a.webhookInfo + `}`)),
		Request:    req,
	}, nil
}

// doctorFixture is a broken install: Telegram delivers to an old address, one member's Google
// sign-in was revoked, the newest backup is days old and RabbitMQ is unreachable. The database is
// real (a throwaway Postgres) and migrated.
type doctorFixture struct {
	cfg       *config.Config
	channels  *channel.Registry
	store     *state.Store
	dbClient  *db.Client
	oauth     *calendar.OAuthConfig
	backupDir string
}

func newDoctorFixture(t *testing.T) doctorFixture {
	t.Helper()
	ctx := context.Background()

	// Redis (Google tokens).
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// Postgres, migrated the way production is.
	dbClient, err := db.Open(pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })
	sqlDB, err := dbClient.DB.DB()
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, sqlDB))

	// Google: renews "good" refresh tokens and refuses the rest.
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("refresh_token") != "good" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(google.Close)
	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://mypa.example/oauth/callback"})
	oauthCfg.SetEndpoint(oauth2.Endpoint{TokenURL: google.URL, AuthStyle: oauth2.AuthStyleInParams})

	// Two families. Only the admin's own may appear in the report.
	householdtest.User(t, dbClient, "1001", "Dana", "admin", "Levi")
	householdtest.User(t, dbClient, "1002", "Omer", "family", "Levi")
	householdtest.User(t, dbClient, "2001", "Stranger", "family", "Cohen")
	token := func(refresh string) []byte {
		return []byte(`{"access_token":"a","refresh_token":"` + refresh + `","token_type":"Bearer","expiry":"2026-11-19T08:00:00Z"}`)
	}
	require.NoError(t, store.SetOAuthToken(ctx, "1001", token("good")))
	require.NoError(t, store.SetOAuthToken(ctx, "1002", token("revoked")))
	require.NoError(t, store.SetOAuthToken(ctx, "2001", token("revoked")))

	// The newest backup is four days old.
	backupDir := t.TempDir()
	old := "backup_" + time.Now().Add(-96*time.Hour).Format("2006-01-02_15-04-05") + ".dump.age"
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, old), []byte("x"), 0o600))

	cfg := &config.Config{}
	cfg.Channels.Telegram.BotToken = "bot-token"
	cfg.Channels.Telegram.WebhookURL = "https://mypa.example/webhook/telegram"
	cfg.Google.ClientID = "id"
	cfg.Channels.WhatsApp.AccountSID = "ACtest"
	cfg.Channels.WhatsApp.FromNumber = "whatsapp:+12025550199"
	cfg.Channels.WhatsApp.StatusCallbackURL = "https://mypa.example/webhook/twilio/status"
	cfg.RabbitMQ.URL = "amqp://guest:guest@127.0.0.1:1/" // nothing listens here

	channels, err := adapters.Build(cfg.Channels, adapters.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://old-tunnel.example/webhook/telegram","pending_update_count":0}`},
	}))
	require.NoError(t, err)
	return doctorFixture{cfg: cfg, channels: channels, store: store, dbClient: dbClient, oauth: oauthCfg, backupDir: backupDir}
}

// V4-17 "done when", through the production wiring: a broken webhook, an expired Google token and
// a stale backup are each detected, each with a next step, and the report stays within the
// admin's family.
func TestHealthReporter_DetectsABrokenInstall(t *testing.T) {
	f := newDoctorFixture(t)
	reporter := newHealthReporter(f.cfg, f.channels, f.store, f.dbClient, f.oauth, f.backupDir)
	admin, err := f.dbClient.GetUser("1001")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report := reporter.Report(ctx, admin)
	t.Log("\n" + report)

	section := func(title string) string {
		t.Helper()
		start := strings.Index(report, "**"+title+"**")
		require.GreaterOrEqual(t, start, 0, "the report has no %q section", title)
		rest := report[start:]
		if end := strings.Index(rest[2:], "\n\n"); end >= 0 {
			rest = rest[:end+2]
		}
		return rest
	}

	// A broken webhook.
	webhook := section("Telegram webhook")
	assert.Contains(t, report, "❌ **Telegram webhook**")
	assert.Contains(t, webhook, "old-tunnel.example")
	assert.Contains(t, webhook, "➡️")

	// An expired Google token, for the member it belongs to.
	omer := section("Google account: Omer")
	assert.Contains(t, report, "❌ **Google account: Omer**")
	assert.Contains(t, omer, "expired or was revoked")
	assert.Contains(t, omer, "➡️ Ask Omer to send /connect")
	assert.Contains(t, report, "✅ **Google account: Dana**")

	// A stale backup.
	backups := section("Backups")
	assert.Contains(t, report, "❌ **Backups**")
	assert.Contains(t, backups, "4 days ago")
	assert.Contains(t, backups, "➡️")

	// An unreachable broker, without leaking its credentials.
	assert.Contains(t, report, "❌ **Queue inbound.messages**")
	assert.NotContains(t, report, "guest:guest")

	// What does work is reported as working.
	assert.Contains(t, report, "✅ **Database schema**")
	assert.Contains(t, report, "✅ **WhatsApp delivery reports**")

	// Tenant isolation: another family's member is not in the report, and no secret is.
	assert.NotContains(t, report, "Stranger")
	assert.NotContains(t, report, "bot-token")
}

// The same install, repaired: nothing fails.
func TestHealthReporter_HealthyInstallHasNoProblems(t *testing.T) {
	f := newDoctorFixture(t)
	ctx := context.Background()
	channels, err := adapters.Build(f.cfg.Channels, adapters.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://mypa.example/webhook/telegram","pending_update_count":0}`},
	}))
	require.NoError(t, err)
	f.channels = channels
	require.NoError(t, f.store.SetOAuthToken(ctx, "1002",
		[]byte(`{"access_token":"a","refresh_token":"good","token_type":"Bearer","expiry":"2026-11-19T08:00:00Z"}`)))
	fresh := "backup_" + time.Now().Add(-3*time.Hour).Format("2006-01-02_15-04-05") + ".dump.age"
	require.NoError(t, os.WriteFile(filepath.Join(f.backupDir, fresh), []byte("x"), 0o600))

	reporter := newHealthReporter(f.cfg, f.channels, f.store, f.dbClient, f.oauth, f.backupDir)
	admin, err := f.dbClient.GetUser("1001")
	require.NoError(t, err)
	report := reporter.Report(ctx, admin)
	t.Log("\n" + report)

	for _, check := range []string{"Telegram webhook", "Google account: Dana", "Google account: Omer", "WhatsApp delivery reports", "Backups", "Database schema"} {
		assert.Contains(t, report, "✅ **"+check+"**")
	}
	// RabbitMQ is still unreachable in this fixture, so exactly the two queue checks fail.
	assert.Equal(t, 2, strings.Count(report, "❌"), "only the queue checks may fail:\n%s", report)
}

// V4-41, through the production wiring and a real database: in sandbox mode /doctor names the
// member of the admin's household whose membership lapsed, from when their account last wrote,
// with the message to send again; another household's member is never named.
func TestHealthReporter_SandboxNamesLapsedMembers(t *testing.T) {
	f := newDoctorFixture(t)
	ctx := context.Background()
	wa := adapterName(t, f.channels, "sandbox")
	householdtest.Member(t, f.dbClient, "+12025550111", "Noa", "family", "Levi", wa)
	householdtest.Member(t, f.dbClient, "+12025550112", "Lior", "family", "Levi", wa)
	householdtest.Member(t, f.dbClient, "+12025550113", "Stranger2", "family", "Cohen", wa)
	setLastInbound := func(id string, at time.Time) {
		require.NoError(t, f.dbClient.DB.Exec(`UPDATE channel_accounts SET last_inbound_at = ? WHERE channel = ? AND external_id = ?`, at, wa, id).Error)
	}
	setLastInbound("+12025550111", time.Now().Add(-5*24*time.Hour))
	setLastInbound("+12025550112", time.Now().Add(-time.Hour))
	setLastInbound("+12025550113", time.Now().Add(-5*24*time.Hour))

	cfg, err := adapters.InMode(f.cfg.Channels, wa, "sandbox")
	require.NoError(t, err)
	cfg.WhatsApp.FromNumber = "whatsapp:+14155238886"
	cfg.WhatsApp.SandboxJoin = "join plenty-tiger"
	f.cfg.Channels = cfg
	f.channels, err = adapters.Build(cfg, adapters.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://mypa.example/webhook/telegram","pending_update_count":0}`},
	}))
	require.NoError(t, err)

	reporter := newHealthReporter(f.cfg, f.channels, f.store, f.dbClient, f.oauth, f.backupDir)
	admin, err := f.dbClient.GetUser("1001")
	require.NoError(t, err)
	report := reporter.Report(ctx, admin)
	t.Log("\n" + report)

	assert.Contains(t, report, "Sandbox, on Twilio's shared number")
	assert.Contains(t, report, "⚠️ **WhatsApp sandbox: Noa**")
	assert.Contains(t, report, "5 days ago")
	assert.Contains(t, report, "`join plenty-tiger`")
	assert.NotContains(t, report, "WhatsApp sandbox: Lior", "a member who wrote within 72 hours is fine")
	assert.Contains(t, report, "**Telegram webhook**", "every adapter keeps its own check")
	assert.NotContains(t, report, "Stranger2", "another household's member is never named")
}

// adapterName is the name of the registered adapter that has the given mode.
func adapterName(t *testing.T, reg *channel.Registry, mode string) string {
	t.Helper()
	for name, modes := range adapters.Modes() {
		for _, m := range modes {
			if m == mode {
				_, ok := reg.Get(name)
				require.True(t, ok)
				return name
			}
		}
	}
	t.Fatalf("no adapter has mode %q", mode)
	return ""
}

// Every shipped adapter has a /doctor check of its own, so a changed check signature cannot drop
// one from the report unnoticed (V4-41 changed it).
func TestChannelChecks_EveryAdapterHasOne(t *testing.T) {
	reg, err := adapters.Build(adapters.Config{})
	require.NoError(t, err)
	for _, a := range reg.All() {
		_, ok := a.(healthChecker)
		assert.True(t, ok, "adapter %q has no HealthCheck(channel.ActivitySource)", a.Name())
	}
	assert.Len(t, channelChecks(reg, nil), len(reg.All()))
}

// approvedTemplates answers Twilio's Content API as for a registered sender whose templates
// WhatsApp approved (V4-41), and hands every other request to next.
type approvedTemplates struct{ next http.RoundTripper }

func (a approvedTemplates) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "content.twilio.com" {
		return a.next.RoundTrip(req)
	}
	if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/ApprovalRequests") {
		return nil, fmt.Errorf("unexpected Content API call %s %s", req.Method, req.URL.Path)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"whatsapp":{"status":"approved","rejection_reason":""}}`)),
		Request:    req,
	}, nil
}
