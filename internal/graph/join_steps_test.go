package graph

import (
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-41: the dashboard shows, next to pairing codes, what a new person does on each channel
// before their first message reaches MyPA (a sandbox's join message). Owners only, like the codes.
func TestJoinSteps_OwnersSeeEachChannelsStep(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	c := resolver.DB
	levi := "Levi-" + uuid.NewString()
	dana := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")
	omer := householdtest.Member(t, c, "1002-"+levi, "Omer", "family", levi, "telegram")

	steps := []channel.JoinStep{{Channel: "whatsapp", Step: "On WhatsApp, first send `join plenty-tiger` to +14155238886."}}
	h := NewAuthMiddleware(c, c)(handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: NewResolver(ResolverConfig{
		DB: c, JoinStepsOf: func() []channel.JoinStep { return steps },
	})})))

	r := gql(t, h, signInAs(t, c, dana), `{ joinSteps { channel step } }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, []any{map[string]any{"channel": "whatsapp", "step": steps[0].Step}}, r.data["joinSteps"])

	assert.Contains(t, gql(t, h, signInAs(t, c, omer), `{ joinSteps { channel } }`, nil).err(), "only an owner")

	steps = nil
	r = gql(t, h, signInAs(t, c, dana), `{ joinSteps { channel } }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, []any{}, r.data["joinSteps"], "no channel needs a step: an empty list, not null")

	unwired := dashboardServer(c)
	r = gql(t, unwired, signInAs(t, c, dana), `{ joinSteps { channel } }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, []any{}, r.data["joinSteps"])
}
