package calendar

import (
	"testing"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestOAuthConfig(t *testing.T) {
	cfg := &config.GoogleConfig{
		ClientID:     "client_id",
		ClientSecret: "client_secret",
		RedirectURL:  "http://localhost/oauth/callback",
	}

	oauthCfg := NewOAuthConfig(cfg)
	assert.NotNil(t, oauthCfg)

	url := oauthCfg.AuthCodeURL("test_state")
	assert.Contains(t, url, "client_id=client_id")
	assert.Contains(t, url, "state=test_state")
	assert.Contains(t, url, "access_type=offline")
	assert.Contains(t, url, "prompt=consent") // approval force adds this
}

func TestTokenEncodeDecode(t *testing.T) {
	token := &oauth2.Token{
		AccessToken:  "access",
		TokenType:    "Bearer",
		RefreshToken: "refresh",
		Expiry:       time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	data, err := EncodeToken(token)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	decoded, err := DecodeToken(data)
	require.NoError(t, err)
	assert.Equal(t, "access", decoded.AccessToken)
	assert.Equal(t, "Bearer", decoded.TokenType)
	assert.Equal(t, "refresh", decoded.RefreshToken)
	assert.True(t, decoded.Expiry.Equal(token.Expiry))
}

func TestDecodeToken_InvalidJSON(t *testing.T) {
	_, err := DecodeToken([]byte(`{invalid`))
	require.Error(t, err)
}
