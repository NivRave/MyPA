package graph

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type AuthClaims struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	FamilyGroup string `json:"family_group"`
	ExpiresAt   int64  `json:"exp,omitempty"`
}

type authContextKey struct{ name string }

var claimsKey = &authContextKey{"claims"}

// ForContext retrieves the authenticated claims from the context.
func ForContext(ctx context.Context) *AuthClaims {
	claims, _ := ctx.Value(claimsKey).(*AuthClaims)
	return claims
}

// GetAuthClaims retrieves the authenticated claims from the context (alias to ForContext).
func GetAuthClaims(ctx context.Context) *AuthClaims {
	return ForContext(ctx)
}

// WithClaims attaches AuthClaims to the context.
func WithClaims(ctx context.Context, claims *AuthClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// NewAuthMiddleware validates Bearer tokens via DASHBOARD_API_SECRET or HMAC-SHA256 JWT
func NewAuthMiddleware(dashboardSecret string, jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized: missing Authorization header"}]}`))
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized: bearer token required"}]}`))
				return
			}
			token := strings.TrimSpace(parts[1])

			// 1. Check Dashboard API Secret (constant-time compare)
			if dashboardSecret != "" && subtle.ConstantTimeCompare([]byte(token), []byte(dashboardSecret)) == 1 {
				claims := &AuthClaims{
					UserID:      "dashboard-service",
					Role:        "admin",
					FamilyGroup: "default",
				}
				next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
				return
			}

			// 2. Check JWT Bearer Token (HMAC-SHA256)
			if jwtSecret != "" {
				claims, err := parseAndVerifyJWT(token, jwtSecret)
				if err == nil && claims != nil {
					next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
					return
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized: invalid credentials"}]}`))
		})
	}
}

func parseAndVerifyJWT(tokenStr, secret string) (*AuthClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, http.ErrNotSupported
	}

	unsignedToken := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsignedToken))
	expectedSig := mac.Sum(nil)

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}

	if subtle.ConstantTimeCompare(sigBytes, expectedSig) != 1 {
		return nil, http.ErrAbortHandler
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims AuthClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}

	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return nil, http.ErrHandlerTimeout
	}

	return &claims, nil
}
