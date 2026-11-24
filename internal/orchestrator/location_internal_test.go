package orchestrator

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
)

// BL-50: a tool's call carries the person's time zone: their household's, else the install's
// DEFAULT_TIMEZONE, as V4-05 resolves it for the scheduler.
func TestCallContext_CarriesThePersonsZone(t *testing.T) {
	store := identitytest.NewStore()
	h := store.Household("bl50-zoned")
	h.TimeZone = "Asia/Jerusalem"
	store.Put(h)
	zoned := store.ScopeIn(t, "bl50-zoned", "p1", "family")
	plain := store.ScopeIn(t, "bl50-plain", "p2", "family")

	e := &Engine{timezone: "America/New_York"}
	call := func(ctx context.Context) string {
		return e.buildCallContext(ctx, &models.User{PlatformID: "p"}, channel.Inbound{Sender: "p"}).Loc().String()
	}
	assert.Equal(t, "Asia/Jerusalem", call(withTurnScope(context.Background(), zoned)), "the household's zone")
	assert.Equal(t, "America/New_York", call(withTurnScope(context.Background(), plain)), "else the install's")
}
