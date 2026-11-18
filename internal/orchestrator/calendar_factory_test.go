package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-1: the factory must tell "never connected" (normal) apart from "token exists but is unusable"
// (an incident), which used to collapse into the same "unauthorized" error.
func TestNewCalendarFactory_DistinguishesNotConnectedFromBrokenToken(t *testing.T) {
	oauth := calendar.NewOAuthConfig(&config.GoogleConfig{ClientID: "id", ClientSecret: "secret"})
	build := func(tok []byte, err error) func(context.Context, string) (CalendarClient, error) {
		return NewCalendarFactory(func(context.Context, string) ([]byte, error) { return tok, err }, oauth)
	}

	_, err := build(nil, nil)(context.Background(), "u1")
	assert.ErrorIs(t, err, ErrCalendarNotConnected, "no stored token = not connected")

	_, err = build(nil, errors.New("decrypt oauth token: cipher: message authentication failed"))(context.Background(), "u1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCalendarNotConnected, "an unreadable token is a failure, not 'not connected'")
	assert.Contains(t, err.Error(), "message authentication failed", "the cause is kept for the log")

	_, err = build([]byte("{not json"), nil)(context.Background(), "u1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCalendarNotConnected)

	client, err := build([]byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer"}`), nil)(context.Background(), "u1")
	require.NoError(t, err)
	assert.NotNil(t, client)
}
