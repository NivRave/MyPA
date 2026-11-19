package graph

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hardDashSecret = "dash-secret-p32"
	hardJWTSecret  = "jwt-secret-p32"
)

func signWithHeader(header map[string]string, claims map[string]interface{}, secret string) string {
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// serve runs the middleware and returns the status plus the claims the handler saw.
func serve(t *testing.T, cfg AuthConfig, token string) (int, *AuthClaims) {
	t.Helper()
	var seen *AuthClaims
	h := NewAuthMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = GetAuthClaims(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, seen
}

func hardCfg() AuthConfig {
	return AuthConfig{DashboardSecret: hardDashSecret, DashboardFamilyGroup: "fam_dash", JWTSecret: hardJWTSecret}
}

func TestJWT_RequiresExpiry(t *testing.T) {
	tok := signWithHeader(map[string]string{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{"user_id": "u1", "role": "family", "family_group": "f1"}, hardJWTSecret)
	code, _ := serve(t, hardCfg(), tok)
	assert.Equal(t, http.StatusUnauthorized, code, "tokens without exp must be rejected")
}

func TestJWT_RejectsExcessiveLifetime(t *testing.T) {
	tok := signWithHeader(map[string]string{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{"user_id": "u1", "role": "family", "family_group": "f1",
			"exp": time.Now().Add(MaxTokenLifetime + time.Hour).Unix()}, hardJWTSecret)
	code, _ := serve(t, hardCfg(), tok)
	assert.Equal(t, http.StatusUnauthorized, code, "exp beyond the max lifetime must be rejected")
}

func TestJWT_RejectsExpired(t *testing.T) {
	tok := signWithHeader(map[string]string{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{"user_id": "u1", "exp": time.Now().Add(-time.Minute).Unix()}, hardJWTSecret)
	code, _ := serve(t, hardCfg(), tok)
	assert.Equal(t, http.StatusUnauthorized, code)
}

// The header's alg must be exactly HS256, even when the HMAC itself verifies.
func TestJWT_RejectsWrongAlgHeader(t *testing.T) {
	for _, alg := range []string{"none", "HS512", "RS256", "hs256", ""} {
		t.Run("alg="+alg, func(t *testing.T) {
			tok := signWithHeader(map[string]string{"alg": alg, "typ": "JWT"},
				map[string]interface{}{"user_id": "u1", "exp": time.Now().Add(time.Hour).Unix()}, hardJWTSecret)
			code, _ := serve(t, hardCfg(), tok)
			assert.Equal(t, http.StatusUnauthorized, code)
		})
	}
}

func TestJWT_ValidTokenAccepted(t *testing.T) {
	tok := signWithHeader(map[string]string{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{"user_id": "u1", "role": "family", "family_group": "f1", "exp": time.Now().Add(time.Hour).Unix()}, hardJWTSecret)
	code, claims := serve(t, hardCfg(), tok)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "u1", claims.UserID)
	assert.False(t, claims.TenantBound)
}

// A JWT payload cannot make itself tenant-bound-exempt or otherwise set server-side flags.
func TestJWT_CannotSetServerSideFlags(t *testing.T) {
	tok := signWithHeader(map[string]string{"alg": "HS256", "typ": "JWT"},
		map[string]interface{}{"user_id": "u1", "role": "family", "TenantBound": true, "tenant_bound": true,
			"exp": time.Now().Add(time.Hour).Unix()}, hardJWTSecret)
	_, claims := serve(t, hardCfg(), tok)
	require.NotNil(t, claims)
	assert.False(t, claims.TenantBound, "TenantBound is set by the server only")
}

// [DECIDE resolved] The dashboard secret is an admin bound to DASHBOARD_FAMILY_GROUP.
func TestDashboardSecret_IsTenantBound(t *testing.T) {
	code, claims := serve(t, hardCfg(), hardDashSecret)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "fam_dash", claims.FamilyGroup)
	assert.True(t, claims.TenantBound)
	assert.False(t, claims.IsGlobalAdmin())
}

func TestDashboardSecret_FailsClosedWithoutFamilyGroup(t *testing.T) {
	cfg := hardCfg()
	cfg.DashboardFamilyGroup = ""
	code, _ := serve(t, cfg, hardDashSecret)
	assert.Equal(t, http.StatusUnauthorized, code, "an unscoped dashboard secret must authenticate nothing")
}

func TestResolveTenantAndActor_NilClaimsIsAnError(t *testing.T) {
	r := &Resolver{}
	fg := "anything"
	_, _, err := r.resolveTenantAndActor(context.Background(), &fg)
	assert.Error(t, err, "missing claims must not default to the requested family group")
}

func TestResolveTenantAndActor_TenantBoundAdminCannotLeaveFamily(t *testing.T) {
	r := &Resolver{Households: labelStore("fam_dash", "fam_other")}
	ctx := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: "fam_dash", TenantBound: true})

	other := "fam_other"
	_, _, err := r.resolveTenantAndActor(ctx, &other)
	assert.Error(t, err)

	own := "fam_dash"
	_, hs, err := r.resolveTenantAndActor(ctx, &own)
	fg := hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "fam_dash", fg)

	_, hs, err = r.resolveTenantAndActor(ctx, nil)
	fg = hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "fam_dash", fg)
}

func TestResolveTenantAndActor_GlobalAdminUnchanged(t *testing.T) {
	r := &Resolver{Households: labelStore("f1", "f2")}
	ctx := WithClaims(context.Background(), &AuthClaims{UserID: "root", Role: "admin", FamilyGroup: "f1"})
	other := "f2"
	_, hs, err := r.resolveTenantAndActor(ctx, &other)
	fg := hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "f2", fg)
}

func TestIsGlobalAdmin(t *testing.T) {
	assert.True(t, (&AuthClaims{Role: "admin"}).IsGlobalAdmin())
	assert.False(t, (&AuthClaims{Role: "admin", TenantBound: true}).IsGlobalAdmin())
	assert.False(t, (&AuthClaims{Role: "family"}).IsGlobalAdmin())
	var nilClaims *AuthClaims
	assert.False(t, nilClaims.IsGlobalAdmin())
}

// Acting on another user's data: global admin anyone; tenant-bound admin only users in its family;
// everyone else only themselves.
func TestCanActOnUser(t *testing.T) {
	familyOf := func(uid string) (string, error) {
		return map[string]string{"alice": "fam_dash", "bob": "fam_other"}[uid], nil
	}
	global := &AuthClaims{UserID: "root", Role: "admin"}
	bound := &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: "fam_dash", TenantBound: true}
	member := &AuthClaims{UserID: "alice", Role: "family", FamilyGroup: "fam_dash"}

	assert.True(t, canActOnUser(global, "", "bob", familyOf))
	assert.True(t, canActOnUser(bound, "fam_dash", "alice", familyOf))
	assert.False(t, canActOnUser(bound, "fam_dash", "bob", familyOf), "tenant-bound admin must not reach other families")
	assert.True(t, canActOnUser(member, "fam_dash", "alice", familyOf))
	assert.False(t, canActOnUser(member, "fam_dash", "bob", familyOf))
	assert.False(t, canActOnUser(nil, "fam_dash", "alice", familyOf))
}
