package main

import (
	"context"
	"fmt"
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
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
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
	tg        *telegram.Client
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
	for _, u := range []models.User{
		{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"},
		{PlatformID: "1002", Name: "Omer", Role: "family", FamilyGroup: "Levi"},
		{PlatformID: "2001", Name: "Stranger", Role: "family", FamilyGroup: "Cohen"},
	} {
		require.NoError(t, dbClient.UpsertUser(u))
	}
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
	cfg.Telegram.BotToken = "bot-token"
	cfg.Telegram.WebhookURL = "https://mypa.example/webhook/telegram"
	cfg.Google.ClientID = "id"
	cfg.Twilio.AccountSID = "ACtest"
	cfg.Twilio.FromNumber = "whatsapp:+12025550199"
	cfg.Twilio.StatusCallbackURL = "https://mypa.example/webhook/twilio/status"
	cfg.RabbitMQ.URL = "amqp://guest:guest@127.0.0.1:1/" // nothing listens here

	tg := telegram.NewClient(cfg.Telegram.BotToken, telegram.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://old-tunnel.example/webhook/telegram","pending_update_count":0}`},
	}))
	return doctorFixture{cfg: cfg, tg: tg, store: store, dbClient: dbClient, oauth: oauthCfg, backupDir: backupDir}
}

// V4-17 "done when", through the production wiring: a broken webhook, an expired Google token and
// a stale backup are each detected, each with a next step, and the report stays within the
// admin's family.
func TestHealthReporter_DetectsABrokenInstall(t *testing.T) {
	f := newDoctorFixture(t)
	reporter := newHealthReporter(f.cfg, f.tg, f.store, f.dbClient, f.oauth, f.backupDir)
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
	assert.Contains(t, report, "✅ **WhatsApp**")

	// Tenant isolation: another family's member is not in the report, and no secret is.
	assert.NotContains(t, report, "Stranger")
	assert.NotContains(t, report, "bot-token")
}

// The same install, repaired: nothing fails.
func TestHealthReporter_HealthyInstallHasNoProblems(t *testing.T) {
	f := newDoctorFixture(t)
	ctx := context.Background()
	f.tg = telegram.NewClient(f.cfg.Telegram.BotToken, telegram.WithHTTPClient(&http.Client{
		Transport: telegramAPI{t: t, webhookInfo: `{"url":"https://mypa.example/webhook/telegram","pending_update_count":0}`},
	}))
	require.NoError(t, f.store.SetOAuthToken(ctx, "1002",
		[]byte(`{"access_token":"a","refresh_token":"good","token_type":"Bearer","expiry":"2026-11-19T08:00:00Z"}`)))
	fresh := "backup_" + time.Now().Add(-3*time.Hour).Format("2006-01-02_15-04-05") + ".dump.age"
	require.NoError(t, os.WriteFile(filepath.Join(f.backupDir, fresh), []byte("x"), 0o600))

	reporter := newHealthReporter(f.cfg, f.tg, f.store, f.dbClient, f.oauth, f.backupDir)
	admin, err := f.dbClient.GetUser("1001")
	require.NoError(t, err)
	report := reporter.Report(ctx, admin)
	t.Log("\n" + report)

	for _, check := range []string{"Telegram webhook", "Google account: Dana", "Google account: Omer", "WhatsApp", "Backups", "Database schema"} {
		assert.Contains(t, report, "✅ **"+check+"**")
	}
	// RabbitMQ is still unreachable in this fixture, so exactly the two queue checks fail.
	assert.Equal(t, 2, strings.Count(report, "❌"), "only the queue checks may fail:\n%s", report)
}
