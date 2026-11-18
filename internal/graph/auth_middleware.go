package graph

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type AuthClaims struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	FamilyGroup string `json:"family_group"`
	ExpiresAt   int64  `json:"exp,omitempty"`

	// TenantBound marks an admin whose powers are confined to FamilyGroup (P3-2: the dashboard
	// secret). Set by the server only — `json:"-"` keeps a JWT payload from setting it.
	TenantBound bool `json:"-"`
}

// IsGlobalAdmin reports whether the caller may act across family groups. Use this — not
// Role == "admin" — for every cross-tenant bypass (P3-2).
func (c *AuthClaims) IsGlobalAdmin() bool {
	return c != nil && c.Role == "admin" && !c.TenantBound
}

// IsAdmin reports admin role regardless of tenant binding (powers within a family).
func (c *AuthClaims) IsAdmin() bool {
	return c != nil && c.Role == "admin"
}

// canActOnUser reports whether the caller may read or modify targetUserID's data: global admins
// anyone; tenant-bound admins only users of their own family; everyone else only themselves.
func canActOnUser(c *AuthClaims, targetUserID string, familyOf func(userID string) (string, error)) bool {
	switch {
	case c == nil:
		return false
	case c.IsGlobalAdmin():
		return true
	case targetUserID == c.UserID:
		return true
	case c.IsAdmin() && c.FamilyGroup != "" && familyOf != nil:
		fg, err := familyOf(targetUserID)
		return err == nil && fg != "" && strings.EqualFold(fg, c.FamilyGroup)
	default:
		return false
	}
}

// MaxTokenLifetime caps how far in the future a JWT's exp may be (P3-2). Long-lived bearer
// tokens are effectively passwords; mint short-lived ones and refresh.
const MaxTokenLifetime = 24 * time.Hour

// AuthConfig configures the /graphql authentication middleware.
type AuthConfig struct {
	// DashboardSecret authenticates the web dashboard as an admin bound to DashboardFamilyGroup.
	DashboardSecret string
	// DashboardFamilyGroup scopes the dashboard secret. Empty disables the secret (fail closed).
	DashboardFamilyGroup string
	// JWTSecret verifies HS256 bearer JWTs.
	JWTSecret string
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

// NewAuthMiddleware validates Bearer tokens via DASHBOARD_API_SECRET (tenant-bound admin) or an
// HS256 JWT with a mandatory, bounded exp.
func NewAuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	dashboardSecret, jwtSecret := cfg.DashboardSecret, cfg.JWTSecret
	dashboardFG := strings.TrimSpace(cfg.DashboardFamilyGroup)
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

			// 1. Check Dashboard API Secret (constant-time compare). P3-2: it is an admin bound to
			// DASHBOARD_FAMILY_GROUP, never a global admin; without a family group it is disabled.
			if dashboardSecret != "" && dashboardFG != "" && subtle.ConstantTimeCompare([]byte(token), []byte(dashboardSecret)) == 1 {
				claims := &AuthClaims{
					UserID:      "dashboard-service",
					Role:        "admin",
					FamilyGroup: dashboardFG,
					TenantBound: true,
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

var (
	errBadAlg      = errors.New("jwt: alg must be HS256")
	errNoExpiry    = errors.New("jwt: exp claim required")
	errTooLongLife = errors.New("jwt: exp exceeds maximum token lifetime")
	errExpired     = errors.New("jwt: token expired")
)

func parseAndVerifyJWT(tokenStr, secret string) (*AuthClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, http.ErrNotSupported
	}

	// P3-2: the header's alg must be exactly HS256 (no "none", no algorithm confusion).
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, err
	}
	if header.Alg != "HS256" {
		return nil, errBadAlg
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

	// P3-2: exp is mandatory and bounded.
	now := time.Now()
	if claims.ExpiresAt <= 0 {
		return nil, errNoExpiry
	}
	if now.Unix() > claims.ExpiresAt {
		return nil, errExpired
	}
	if time.Unix(claims.ExpiresAt, 0).Sub(now) > MaxTokenLifetime {
		return nil, errTooLongLife
	}
	claims.TenantBound = false // server-side flag; never from the token

	return &claims, nil
}
