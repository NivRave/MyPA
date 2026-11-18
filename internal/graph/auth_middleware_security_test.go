package graph

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdversarial_SEC02_AuthMiddleware_HeaderVariations(t *testing.T) {
	dashboardSecret := "prod-dashboard-secret-999"
	jwtSecret := "prod-jwt-secret-888"
	middleware := NewAuthMiddleware(AuthConfig{DashboardSecret: dashboardSecret, DashboardFamilyGroup: "TestFamily", JWTSecret: jwtSecret})

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"success"}`))
	})
	server := middleware(dummyHandler)

	tests := []struct {
		name        string
		authHeader  string
		expectedErr string
	}{
		{"EmptyHeader", "", "missing Authorization header"},
		{"BasicAuthScheme", "Basic dXNlcjpwYXNz", "bearer token required"},
		{"TokenScheme", "Token abcdef12345", "bearer token required"},
		{"ApiKeyScheme", "ApiKey xyz", "bearer token required"},
		{"BearerOnly", "Bearer", "bearer token required"},
		{"BearerEmptyToken", "Bearer   ", "invalid credentials"},
		{"InvalidDashboardSecret", "Bearer wrong-secret", "invalid credentials"},
		{"SubtlePrefixMatchDashboardSecret", "Bearer prod-dashboard-secret", "invalid credentials"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/graphql", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			server.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Contains(t, rr.Body.String(), tc.expectedErr)
		})
	}
}

func TestAdversarial_SEC02_AuthMiddleware_JWTForgingAndAttacks(t *testing.T) {
	dashboardSecret := "prod-dashboard-secret-999"
	jwtSecret := "prod-jwt-secret-888"
	middleware := NewAuthMiddleware(AuthConfig{DashboardSecret: dashboardSecret, DashboardFamilyGroup: "TestFamily", JWTSecret: jwtSecret})

	var capturedClaims *AuthClaims
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = ForContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"success"}`))
	})
	server := middleware(dummyHandler)

	// 1. Alg "none" attack
	t.Run("AlgNoneAttack", func(t *testing.T) {
		capturedClaims = nil
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":"hacker","role":"admin"}`))
		forgedToken := header + "." + payload + "."

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+forgedToken)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Nil(t, capturedClaims)
	})

	// 2. Tampered Payload (Privilege escalation: change role from family to admin)
	t.Run("TamperedPayloadEscalation", func(t *testing.T) {
		capturedClaims = nil
		legitToken := generateTestJWT(AuthClaims{
			UserID: "victim-user",
			Role:   "family",
		}, jwtSecret)

		tokenParts := strings.Split(legitToken, ".")
		require.Len(t, tokenParts, 3)

		// Create forged payload claiming admin role
		tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":"victim-user","role":"admin"}`))
		// Tampered token pairs header and original signature with new payload
		tamperedToken := tokenParts[0] + "." + tamperedPayload + "." + tokenParts[2]

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+tamperedToken)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Nil(t, capturedClaims)
	})

	// 3. Expired JWT
	t.Run("ExpiredToken", func(t *testing.T) {
		capturedClaims = nil
		expiredToken := generateTestJWT(AuthClaims{
			UserID:    "expired-user",
			Role:      "admin",
			ExpiresAt: time.Now().Add(-10 * time.Minute).Unix(),
		}, jwtSecret)

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+expiredToken)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Nil(t, capturedClaims)
	})

	// 4. Valid Dashboard Secret Access
	t.Run("ValidDashboardSecret", func(t *testing.T) {
		capturedClaims = nil
		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+dashboardSecret)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "dashboard-service", capturedClaims.UserID)
		assert.Equal(t, "admin", capturedClaims.Role)
	})

	// 5. Valid JWT Access
	t.Run("ValidJWTClaims", func(t *testing.T) {
		capturedClaims = nil
		validToken := generateTestJWT(AuthClaims{
			UserID:      "legit-user",
			Role:        "admin",
			FamilyGroup: "TestFamily",
			ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
		}, jwtSecret)

		req := httptest.NewRequest("POST", "/graphql", nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedClaims)
		assert.Equal(t, "legit-user", capturedClaims.UserID)
		assert.Equal(t, "admin", capturedClaims.Role)
		assert.Equal(t, "TestFamily", capturedClaims.FamilyGroup)
	})
}

func TestAdversarial_SEC02_AuthMiddleware_EmptySecretsRejectsAll(t *testing.T) {
	// If server misconfigured with empty secrets, all requests must be rejected
	middleware := NewAuthMiddleware(AuthConfig{})
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := middleware(dummyHandler)

	req := httptest.NewRequest("POST", "/graphql", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid credentials")
}
