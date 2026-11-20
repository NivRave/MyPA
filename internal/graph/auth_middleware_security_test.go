package graph

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SEC-02 for the session middleware (V4-35): every header that is not a live session's secret is
// refused, and the store is not even asked for one that is not well formed.
func TestAdversarial_SEC02_AuthMiddleware_HeaderVariations(t *testing.T) {
	f := newFakeSessions()
	secret := f.open(t, "TestFamily", "p-1", identity.RoleOwner)
	other, err := identity.NewSecret()
	require.NoError(t, err)

	cases := map[string]string{
		"EmptyHeader":            "",
		"BasicAuthScheme":        "Basic dXNlcjpwYXNz",
		"TokenScheme":            "Token " + secret,
		"BearerOnly":             "Bearer",
		"BearerEmptyToken":       "Bearer   ",
		"CookieValueNotBearer":   "mypa_session=" + secret,
		"PrefixOfTheSecret":      "Bearer " + secret[:42],
		"SecretWithExtraChar":    "Bearer " + secret + "A",
		"TheHashInsteadOfSecret": "Bearer " + identity.HashSecret(secret),
		"WellFormedButUnknown":   "Bearer " + other,
		"SQLInjection":           "Bearer ' OR 1=1 --",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			code, c := serveAs(t, f, header)
			assert.Equal(t, http.StatusUnauthorized, code)
			assert.Nil(t, c, "the handler must not run")
		})
	}

	f.mu.Lock()
	before := f.lookups
	f.mu.Unlock()
	for _, h := range []string{"Bearer short", "Bearer " + strings.Repeat("!", 43), "Bearer " + identity.HashSecret(secret)} {
		serveAs(t, f, h)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, before, f.lookups, "a malformed secret is refused before any lookup")
}

// signJWT makes an HS256 token as the removed signed-token path accepted it.
func signJWT(t *testing.T, secret, alg string, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	h, err := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	require.NoError(t, err)
	p, err := json.Marshal(claims)
	require.NoError(t, err)
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(m.Sum(nil))
}

// V4-35 done-when: the shared dashboard secret no longer grants access, and neither does a signed
// token, whoever holds the old secrets. Before V4-35 each of these was accepted as an admin.
func TestAdversarial_SharedSecretAndSignedTokensNoLongerGrantAccess(t *testing.T) {
	f := newFakeSessions()
	f.open(t, "TestFamily", "p-1", identity.RoleOwner)
	const oldDashboardSecret = "prod-dashboard-secret-999"
	const oldJWTSecret = "prod-jwt-secret-888"
	t.Setenv("DASHBOARD_API_SECRET", oldDashboardSecret)
	t.Setenv("DASHBOARD_FAMILY_GROUP", "TestFamily")
	t.Setenv("JWT_SECRET", oldJWTSecret)

	admin := map[string]any{"user_id": "root", "role": "admin", "family_group": "TestFamily", "exp": time.Now().Add(time.Hour).Unix()}
	cases := map[string]string{
		"TheOldDashboardSecret":          "Bearer " + oldDashboardSecret,
		"TheDevelopmentFallbackToken":    "Bearer dev-dashboard-token",
		"AValidHS256AdminToken":          "Bearer " + signJWT(t, oldJWTSecret, "HS256", admin),
		"AnAlgNoneToken":                 "Bearer " + signJWT(t, "", "none", admin),
		"ATokenSignedWithTheDashSecret":  "Bearer " + signJWT(t, oldDashboardSecret, "HS256", admin),
		"ATenantBoundClaimInTheToken":    "Bearer " + signJWT(t, oldJWTSecret, "HS256", map[string]any{"role": "admin", "TenantBound": true, "exp": admin["exp"]}),
		"TheOldSecretWithBasicScheme":    "Basic " + base64.StdEncoding.EncodeToString([]byte("dashboard:"+oldDashboardSecret)),
		"TheOldSecretPaddedToSecretSize": "Bearer " + (oldDashboardSecret + strings.Repeat("A", 43))[:43],
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			code, c := serveAs(t, f, header)
			assert.Equal(t, http.StatusUnauthorized, code)
			assert.Nil(t, c)
		})
	}
}
