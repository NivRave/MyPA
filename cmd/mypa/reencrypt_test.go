package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/crypto/cryptotest"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/reencrypt"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reencryptRedisDB is the Redis database this test uses, apart from the other tests' database 0.
const reencryptRedisDB = 7

// V4-07 done-when: "an old-format value in each store is rewritten, then decrypts without the
// fallback, and a second run reports zero". The three stores are the real ones (Redis, and
// Postgres at this build's schema), driven through the command. A dry run only counts. Values
// already in the current format, values no key opens, and OP-2's legacy-token:* keys are left as
// they are.
func TestReencrypt_RewritesTheOldFormatInEveryStoreThenReportsZero(t *testing.T) {
	const key = "reencrypt-test-key"
	t.Setenv("ENCRYPTION_KEY", key)
	ctx := context.Background()

	client, err := db.Open(pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	sqlDB, err := client.DB.DB()
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, sqlDB))

	addr, err := testutil.GetRedisAddr(ctx)
	require.NoError(t, err)
	raw := redis.NewClient(&redis.Options{Addr: addr, DB: reencryptRedisDB})
	require.NoError(t, raw.FlushDB(ctx).Err())
	t.Cleanup(func() { _ = raw.FlushDB(context.Background()); _ = raw.Close() })
	cache, err := state.NewStore("redis://" + addr + "/7")
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	// One old-format value per store, next to one in the current format.
	danaToken, omerToken := []byte(`{"access_token":"dana"}`), []byte(`{"access_token":"omer"}`)
	headers := []byte(`{"Authorization":"Bearer weather"}`)
	oldToken := cryptotest.Unsalted(t, danaToken, key)
	currentToken, err := crypto.Encrypt(omerToken, key)
	require.NoError(t, err)
	require.NoError(t, raw.Set(ctx, "token:1001", oldToken, 6*time.Hour).Err())
	require.NoError(t, raw.Set(ctx, "token:1002", currentToken, 0).Err())
	require.NoError(t, raw.Set(ctx, "token:1003", "not a ciphertext", 0).Err())
	otp2Key := cryptotest.Unsalted(t, danaToken, "the old public fallback key")
	require.NoError(t, raw.Set(ctx, "legacy-token:1001", otp2Key, 0).Err())

	oldCredential := cryptotest.Unsalted(t, danaToken, key)
	currentCredential, err := crypto.Encrypt(omerToken, key)
	require.NoError(t, err)
	require.NoError(t, client.SaveEncryptedCredential(models.EncryptedCredential{UserID: "1001", Provider: "google_oauth", Ciphertext: oldCredential}))
	require.NoError(t, client.SaveEncryptedCredential(models.EncryptedCredential{UserID: "1002", Provider: "google_oauth", Ciphertext: currentCredential}))

	levi := householdtest.Household(t, client, "Levi").ID
	oldHeaders := cryptotest.Unsalted(t, headers, key)
	currentHeaders, err := crypto.Encrypt(headers, key)
	require.NoError(t, err)
	oldConnector := models.ExternalAPIConnector{UserID: "1001", HouseholdID: levi, FamilyGroup: "Levi", Name: "weather", EncryptedAuthHeaders: oldHeaders}
	currentConnector := models.ExternalAPIConnector{UserID: "1001", HouseholdID: levi, FamilyGroup: "Levi", Name: "calendar", EncryptedAuthHeaders: currentHeaders}
	require.NoError(t, client.DB.Create(&oldConnector).Error)
	require.NoError(t, client.DB.Create(&currentConnector).Error)

	token := func(k string) []byte {
		b, err := raw.Get(ctx, k).Bytes()
		require.NoError(t, err)
		return b
	}
	credential := func(user string) []byte {
		c, err := client.GetEncryptedCredential(user, "google_oauth")
		require.NoError(t, err)
		return c.Ciphertext
	}
	connector := func(id uint) []byte {
		var c models.ExternalAPIConnector
		require.NoError(t, client.DB.First(&c, id).Error)
		return c.EncryptedAuthHeaders
	}
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		deps := reencryptDeps{
			Stores: []reencrypt.Store{cache.TokenStore(), db.CredentialFallbackStore(sqlDB), db.ConnectorHeadersStore(sqlDB)},
			Out:    &out,
		}
		code := runReencrypt(ctx, args, deps)
		t.Log("\n" + out.String())
		return code, out.String()
	}

	// A dry run counts one old-format value in each store and changes nothing.
	code, report := run("--dry-run")
	require.Zero(t, code, report)
	assert.Equal(t, 3, strings.Count(report, "old format found:     1"), "one old-format value in each of the three stores")
	assert.Equal(t, 3, strings.Count(report, "old format remaining: 1"))
	assert.Contains(t, report, "! not readable with this key: 1")
	assert.Equal(t, oldToken, token("token:1001"))
	assert.Equal(t, oldCredential, credential("1001"))
	assert.Equal(t, oldHeaders, connector(oldConnector.ID))

	// The pass rewrites each of them.
	code, report = run()
	require.Zero(t, code, report)
	assert.Equal(t, 3, strings.Count(report, "rewritten:            1"), "the old-format value of each store is rewritten")
	assert.Equal(t, 3, strings.Count(report, "old format remaining: 0"))
	assert.Contains(t, report, "✓ No old-format value remains in any store.")

	// Each rewritten value decrypts without the fallback, to what it held before.
	for name, c := range map[string]struct {
		value, plaintext []byte
	}{
		"Redis token":      {token("token:1001"), danaToken},
		"credential copy":  {credential("1001"), danaToken},
		"connector header": {connector(oldConnector.ID), headers},
	} {
		got, err := crypto.DecryptCurrent(c.value, "")
		require.NoError(t, err, "%s decrypts without the fallback", name)
		assert.Equal(t, c.plaintext, got, name)
		_, format, err := crypto.Inspect(c.value, "")
		require.NoError(t, err)
		assert.Equal(t, crypto.FormatCurrent, format, name)
	}
	ttl, err := raw.TTL(ctx, "token:1001").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 5*time.Hour, "the token keeps its expiry")
	got, err := cache.GetOAuthToken(ctx, "1001")
	require.NoError(t, err)
	assert.Equal(t, danaToken, got, "the app reads the rewritten sign-in")

	// What was not old-format is untouched.
	assert.Equal(t, currentToken, token("token:1002"))
	assert.Equal(t, []byte("not a ciphertext"), token("token:1003"))
	assert.Equal(t, otp2Key, token("legacy-token:1001"), "OP-2's keys are a separate clean-up")
	assert.Equal(t, currentCredential, credential("1002"))
	assert.Equal(t, currentHeaders, connector(currentConnector.ID))

	// A second run reports zero.
	code, report = run()
	require.Zero(t, code, report)
	assert.Equal(t, 3, strings.Count(report, "old format found:     0"))
	assert.Equal(t, 3, strings.Count(report, "rewritten:            0"))
	assert.Equal(t, 3, strings.Count(report, "old format remaining: 0"))
	assert.Contains(t, report, "✓ No old-format value remains in any store.")
}

func TestReencrypt_Usage(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 2, runReencrypt(context.Background(), []string{"--force"}, reencryptDeps{Out: &out}))
	assert.Contains(t, out.String(), "Usage: mypa reencrypt [--dry-run]")
	out.Reset()
	assert.Equal(t, 2, runReencrypt(context.Background(), []string{"extra"}, reencryptDeps{Out: &out}))
}

// stuckStore never manages to replace a value, as when the app writes each one again first.
type stuckStore struct{ value []byte }

func (s stuckStore) Name() string { return "stuck" }
func (s stuckStore) Each(_ context.Context, fn func(string, []byte) error) error {
	return fn("k", s.value)
}
func (s stuckStore) Replace(context.Context, string, []byte, []byte) (bool, error) { return false, nil }

// A run that leaves an old-format value fails, so a script or the owner cannot mistake it for
// the zero that V4-45 waits for. A dry run that finds one does not fail.
func TestReencrypt_LeftoverOldFormatFailsTheRun(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "leftover-key")
	deps := reencryptDeps{Stores: []reencrypt.Store{stuckStore{cryptotest.Unsalted(t, []byte("x"), "leftover-key")}}}
	var out bytes.Buffer
	deps.Out = &out
	assert.Equal(t, 1, runReencrypt(context.Background(), nil, deps))
	assert.Contains(t, out.String(), "1 old-format value(s) remain")
	out.Reset()
	assert.Equal(t, 0, runReencrypt(context.Background(), []string{"--dry-run"}, deps))
	assert.Contains(t, out.String(), "would be rewritten")
}
