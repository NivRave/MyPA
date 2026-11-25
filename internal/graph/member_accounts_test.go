package graph

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sentNotice is one message the dashboard asked to be sent: to one account, or to a person by key.
type sentNotice struct{ to, text string }

// recordingAnnouncer records what an account change announces instead of sending it.
type recordingAnnouncer struct {
	mu   sync.Mutex
	sent []sentNotice
}

func (a *recordingAnnouncer) SendToAccount(_ context.Context, acct models.ChannelAccount, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, sentNotice{to: "account:" + acct.ID, text: text})
	return nil
}

func (a *recordingAnnouncer) SendMessage(_ context.Context, key, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, sentNotice{to: "person:" + key, text: text})
	return nil
}

func (a *recordingAnnouncer) notices() []sentNotice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sentNotice(nil), a.sent...)
}

const removeAccountMutation = `mutation($p: ID!, $a: ID!) { removeMemberAccount(personId: $p, accountId: $a) }`

// BL-41, ADR 0002 Decision 4, end to end through the real middleware and schema: an owner removes
// a member's second account from the dashboard. It is gone and can no longer act as the person; the
// person's remaining account and the owners are told. The last account is refused; an adult, a
// child, a guest and an owner of another household are refused, and nothing is removed or announced.
func TestRemoveMemberAccount_OwnerRemovesAMembersSecondAccount(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	c := resolver.DB
	ann := &recordingAnnouncer{}
	h := NewAuthMiddleware(c, c)(handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: NewResolver(ResolverConfig{DB: c, Announcer: ann})})))
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	cohen := "Cohen-" + uuid.NewString()

	dana := householdtest.Member(t, c, "1001-"+levi, "Dana", "owner", levi, "telegram")
	omer := householdtest.Member(t, c, "1002-"+levi, "Omer", "adult", levi, "telegram")
	kid := householdtest.Member(t, c, "1004-"+levi, "Kid", "child", levi, "telegram")
	sitter := householdtest.Member(t, c, "1005-"+levi, "Sitter", "guest", levi, "telegram")
	other := householdtest.Member(t, c, "2001-"+cohen, "Ran", "owner", cohen, "telegram")
	first, err := c.AccountOf(ctx, "telegram", omer.PlatformID)
	require.NoError(t, err)
	require.NotNil(t, first)
	second := models.ChannelAccount{PersonID: omer.ID, Channel: "whatsapp", ExternalID: "+1555" + uuid.NewString()[:7], LinkedAt: time.Now()}
	require.NoError(t, c.DB.Create(&second).Error)

	S := map[string]string{}
	for name, u := range map[string]models.User{"dana": dana, "omer": omer, "kid": kid, "sitter": sitter, "other": other} {
		S[name] = signInAs(t, c, u)
	}
	vars := map[string]any{"p": omer.ID, "a": second.ID}

	// Everyone but an owner of the household is refused, and nothing happens.
	for _, name := range []string{"omer", "kid", "sitter", "other"} {
		r := gql(t, h, S[name], removeAccountMutation, vars)
		assert.NotEmpty(t, r.errs, "%s may not remove a member's account", name)
		r = gql(t, h, S[name], `query($p: ID!) { memberAccounts(personId: $p) { id } }`, map[string]any{"p": omer.ID})
		assert.NotEmpty(t, r.errs, "%s may not list a member's accounts", name)
	}
	still, err := c.AccountOf(ctx, "whatsapp", second.ExternalID)
	require.NoError(t, err)
	require.NotNil(t, still, "a refusal removes nothing")
	assert.Empty(t, ann.notices(), "a refusal announces nothing")

	// The owner sees the member's accounts, by channel and the end of the number only.
	r := gql(t, h, S["dana"], `query($p: ID!) { memberAccounts(personId: $p) { id channel label linkedAt } }`, map[string]any{"p": omer.ID})
	require.Empty(t, r.errs)
	listed := r.data["memberAccounts"].([]any)
	require.Len(t, listed, 2)
	assert.Equal(t, first.ID, listed[0].(map[string]any)["id"], "oldest first")
	assert.Equal(t, identity.Describe(second.Channel, second.ExternalID), listed[1].(map[string]any)["label"])
	assert.NotContains(t, listed[1].(map[string]any)["label"], second.ExternalID)

	// The owner removes the second account.
	r = gql(t, h, S["dana"], removeAccountMutation, vars)
	require.Empty(t, r.errs)
	assert.Equal(t, true, r.data["removeMemberAccount"])
	gone, err := c.AccountOf(ctx, "whatsapp", second.ExternalID)
	require.NoError(t, err)
	assert.Nil(t, gone, "the account is gone")
	_, err = c.UserForAccount(ctx, "whatsapp", second.ExternalID)
	assert.Error(t, err, "the removed account can no longer act as the person")
	u, err := c.UserForAccount(ctx, "telegram", omer.PlatformID)
	require.NoError(t, err, "the remaining account still is the person")
	assert.Equal(t, omer.ID, u.ID)

	desc := identity.Describe(second.Channel, second.ExternalID)
	got := map[string]string{}
	for _, n := range ann.notices() {
		got[n.to] = n.text
		assert.NotContains(t, n.text, second.ExternalID, "the announcement names the account by its end only")
	}
	assert.Contains(t, got["account:"+first.ID], desc, "the person's remaining account is told")
	assert.Contains(t, got["person:"+dana.PlatformID], desc, "the owners are told")
	_, toRemoved := got["account:"+second.ID]
	assert.False(t, toRemoved, "nothing goes to the removed account")
	_, toStranger := got["person:"+other.PlatformID]
	assert.False(t, toStranger, "another household's owner is not told")

	// The last account cannot be removed.
	before := len(ann.notices())
	r = gql(t, h, S["dana"], removeAccountMutation, map[string]any{"p": omer.ID, "a": first.ID})
	assert.Contains(t, strings.ToLower(r.err()), "last account")
	left, err := c.AccountsOfPerson(ctx, omer.ID)
	require.NoError(t, err)
	assert.Len(t, left, 1)
	assert.Len(t, ann.notices(), before, "a refusal announces nothing")
}
