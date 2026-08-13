package calendar

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nivik/mypa/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

// OAuthConfig holds the OAuth2 configuration.
type OAuthConfig struct {
	config *oauth2.Config
}

// NewOAuthConfig initializes the Google OAuth2 config.
func NewOAuthConfig(cfg *config.GoogleConfig) *OAuthConfig {
	return &OAuthConfig{
		config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{calendar.CalendarEventsScope},
		},
	}
}

// AuthCodeURL generates a URL for the user to log in and authorize the app.
func (o *OAuthConfig) AuthCodeURL(state string) string {
	// We use the state parameter to pass the UserID so we know who logged in.
	return o.config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// Exchange converts an authorization code into an OAuth token.
func (o *OAuthConfig) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return o.config.Exchange(ctx, code)
}

// TokenSource creates a token source that automatically refreshes the token if necessary.
func (o *OAuthConfig) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return o.config.TokenSource(ctx, token)
}

// EncodeToken serializes a token to JSON bytes for storage.
func EncodeToken(token *oauth2.Token) ([]byte, error) {
	return json.Marshal(token)
}

// DecodeToken deserializes a token from JSON bytes.
func DecodeToken(data []byte) (*oauth2.Token, error) {
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}
	return &token, nil
}
