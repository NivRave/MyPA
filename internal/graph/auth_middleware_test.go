package graph

import (
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

func generateTestJWT(claims AuthClaims, secret string) string {
	headerJSON, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payloadJSON, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	unsignedToken := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsignedToken))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return unsignedToken + "." + sigB64
}

func TestAuthMiddleware(t *testing.T) {
	dashboardSecret := "test-dashboard-secret-xyz"
	jwtSecret := "test-jwt-secret-12345"
	middleware := NewAuthMiddleware(AuthConfig{DashboardSecret: dashboardSecret, DashboardFamilyGroup: "TestFamily", JWTSecret: jwtSecret})

	var capturedClaims *AuthClaims
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = ForContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"ok"}`))
	})

	server := middleware(dummyHandler)

	// 1. Missing Authorization header -> 401
	{
		capturedClaims = nil
		req := httptest.NewRequest("POST", "/graphql", nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "missing Authorization header")
		assert.Nil(t, capturedClaims)
	}

	// 2. Non-bearer format -> 401
	{
		capturedClaims = nil
		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Basic user:pass")
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "bearer token required")
		assert.Nil(t, capturedClaims)
	}

	// 3. Valid Dashboard API Secret -> 200, admin claims
	{
		capturedClaims = nil
		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+dashboardSecret)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "dashboard-service", capturedClaims.UserID)
		assert.Equal(t, "admin", capturedClaims.Role)
	}

	// 4. Valid JWT -> 200, user claims
	{
		capturedClaims = nil
		jwtToken := generateTestJWT(AuthClaims{
			UserID:      "user-jwt-1",
			Role:        "family",
			FamilyGroup: "Smiths",
			ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
		}, jwtSecret)

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "user-jwt-1", capturedClaims.UserID)
		assert.Equal(t, "family", capturedClaims.Role)
		assert.Equal(t, "Smiths", capturedClaims.FamilyGroup)
	}

	// 5. Expired JWT -> 401
	{
		capturedClaims = nil
		expiredJWT := generateTestJWT(AuthClaims{
			UserID:      "user-expired",
			Role:        "family",
			FamilyGroup: "Smiths",
			ExpiresAt:   time.Now().Add(-1 * time.Hour).Unix(),
		}, jwtSecret)

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+expiredJWT)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Nil(t, capturedClaims)
	}

	// 6. Bad signature JWT -> 401
	{
		capturedClaims = nil
		badSigJWT := generateTestJWT(AuthClaims{
			UserID:    "user-tampered",
			Role:      "admin",
			ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
		}, "wrong-secret")

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+badSigJWT)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Nil(t, capturedClaims)
	}
}
