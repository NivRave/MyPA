package db

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personWithAccount creates a user (through the production path), their person and one account,
// linked at linkedAt.
func personWithAccount(t *testing.T, c *Client, label, platformID, name, role, channelName string, linkedAt time.Time) (*models.User, models.ChannelAccount) {
	t.Helper()
	h := householdFor(t, c, label)
	require.NoError(t, c.UpsertUser(models.User{PlatformID: platformID, Name: name, Role: role, HouseholdID: h.ID, FamilyGroup: label}))
	u, err := c.GetUser(platformID)
	require.NoError(t, err)
	a := models.ChannelAccount{PersonID: u.ID, Channel: channelName, ExternalID: platformID, LinkedAt: linkedAt}
	require.NoError(t, c.DB.Create(&a).Error)
	require.NoError(t, c.DB.Exec(`UPDATE people SET preferred_account_id = ? WHERE id = ?`, a.ID, u.ID).Error)
	return u, a
}

func scopeOfUser(t *testing.T, c *Client, u *models.User) identity.Scope {
	t.Helper()
	s, err := identity.ForUser(context.Background(), c, u)
	require.NoError(t, err)
	return s
}

var testCodeKey = []byte("0123456789abcdef0123456789abcdef")

// ADR 0002 Decision 4: a code is claimed in one statement, so two accounts racing for it cannot
// both get it; a claimed code links nothing until the issuing account confirms, only that account
// can, and only once.
func TestLinkCode_ClaimedOnceConfirmedOnlyByTheIssuer(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", old)
	_, other := personWithAccount(t, c, "Levi", "7009", "Other", "family", "telegram", old)

	hash := identity.HashCode(testCodeKey, "ABCD2345")
	require.NoError(t, c.IssueLinkCode(ctx, scopeOfUser(t, c, alice), tg, hash))

	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := c.ClaimLinkCode(ctx, hash, "whatsapp", "+1555000000"+string(rune('0'+i))); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else {
				assert.ErrorIs(t, err, identity.ErrCodeInvalid)
			}
		}(i)
	}
	wg.Wait()
	assert.EqualValues(t, 1, wins, "exactly one account claims a code")

	tok, err := c.PendingLink(ctx, tg.ID)
	require.NoError(t, err)
	_, err = c.PendingLink(ctx, other.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "another account sees nothing to confirm")
	_, err = c.ConfirmLink(ctx, tok.ID, other.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "only the issuing account can confirm")

	added, err := c.ConfirmLink(ctx, tok.ID, tg.ID)
	require.NoError(t, err)
	assert.Equal(t, alice.ID, added.PersonID)
	require.NotNil(t, added.LastInboundAt, "the new account wrote when it typed the code")
	u, err := c.UserForAccount(ctx, added.Channel, added.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, alice.ID, u.ID, "the new account is Alice")

	_, err = c.ConfirmLink(ctx, tok.ID, tg.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "a code links once")
	_, err = c.ClaimLinkCode(ctx, hash, "whatsapp", "+15550009999")
	assert.ErrorIs(t, err, identity.ErrCodeInvalid, "a used code cannot be claimed again")
}

// A code expires; a new code revokes the person's older one; a code is only valid as a link code.
func TestLinkCode_ExpiresAndIsReplaced(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", time.Now().Add(-72*time.Hour))
	s := scopeOfUser(t, c, alice)

	first := identity.HashCode(testCodeKey, "AAAA1111")
	second := identity.HashCode(testCodeKey, "BBBB2222")
	require.NoError(t, c.IssueLinkCode(ctx, s, tg, first))
	require.NoError(t, c.IssueLinkCode(ctx, s, tg, second))
	_, err := c.ClaimLinkCode(ctx, first, "whatsapp", "+15550001001")
	assert.ErrorIs(t, err, identity.ErrCodeInvalid, "a new code revokes the older one")

	require.NoError(t, c.DB.Model(&models.AccessToken{}).Where("token_hash = ?", second).Update("expires_at", time.Now().Add(-time.Second)).Error)
	_, err = c.ClaimLinkCode(ctx, second, "whatsapp", "+15550001001")
	assert.ErrorIs(t, err, identity.ErrCodeInvalid, "an expired code cannot be claimed")

	// A claimed code waits LinkCodeTTL for its confirmation, then nothing is pending.
	third := identity.HashCode(testCodeKey, "CCCC3333")
	require.NoError(t, c.IssueLinkCode(ctx, s, tg, third))
	tok, err := c.ClaimLinkCode(ctx, third, "whatsapp", "+15550001001")
	require.NoError(t, err)
	require.NoError(t, c.DB.Model(&models.AccessToken{}).Where("id = ?", tok.ID).Update("claimed_at", time.Now().Add(-identity.LinkCodeTTL-time.Second)).Error)
	_, err = c.PendingLink(ctx, tg.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending)
	_, err = c.ConfirmLink(ctx, tok.ID, tg.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "a confirmation after the window links nothing")

	// The household of the code is the issuer's: a scope of another household cannot issue for them.
	other := scopeAs(t, c, "Cohen", "8001", "admin")
	assert.Error(t, c.IssueLinkCode(ctx, other, tg, identity.HashCode(testCodeKey, "DDDD4444")))
}

// An account that joined someone while its code waited is not linked again.
func TestConfirmLink_RefusesAnAccountTakenMeanwhile(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", old)
	hash := identity.HashCode(testCodeKey, "EEEE5555")
	require.NoError(t, c.IssueLinkCode(ctx, scopeOfUser(t, c, alice), tg, hash))
	tok, err := c.ClaimLinkCode(ctx, hash, "whatsapp", "+15550002002")
	require.NoError(t, err)
	personWithAccount(t, c, "Levi", "+15550002002", "Bob", "family", "whatsapp", old)

	_, err = c.ConfirmLink(ctx, tok.ID, tg.ID)
	assert.ErrorIs(t, err, identity.ErrAccountTaken)
	u, err := c.UserForAccount(ctx, "whatsapp", "+15550002002")
	require.NoError(t, err)
	assert.Equal(t, "Bob", u.Name, "Bob keeps his account")
}

// ADR 0002 question 4 and Decision 4: two people are merged only when that human links them,
// inside one household, with a confirmation from both sides: neither the issuer nor the account
// that typed the code can merge alone, so nobody pulls another person's account into theirs by
// getting them to type a code. The merged person's accounts and data become the survivor's; the
// rows only one of them can keep are resolved; the merged person is gone.
func TestMergeOnLink_OneHouseholdOnlyAndTheDataFollows(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", old)
	alias, wa := personWithAccount(t, c, "Levi", "+15550003003", "Alice WhatsApp", "family", "whatsapp", old)
	stranger, sx := personWithAccount(t, c, "Cohen", "+15550004004", "Stranger", "family", "whatsapp", old)

	// Data written under each old key.
	aliceScope, aliasScope := scopeOfUser(t, c, alice), scopeOfUser(t, c, alias)
	repo := NewMemoryV2Repo(c.DB)
	mem := models.TopicMemory{Topic: "health", Fact: "allergic to penicillin", Scope: "personal", UserID: alias.PlatformID}
	require.NoError(t, repo.SaveTopicMemory(ctx, aliasScope, &mem))
	require.NoError(t, c.DB.Create(&models.UserPreference{UserID: alice.PlatformID, PreferredLanguage: "he", BriefingTopicsJSON: "[]"}).Error)
	require.NoError(t, c.DB.Create(&models.UserPreference{UserID: alias.PlatformID, PreferredLanguage: "en", TonePersona: "brief", BriefingTopicsJSON: "[]"}).Error)

	// Across households: refused, nothing changes.
	hash := identity.HashCode(testCodeKey, "FFFF6666")
	require.NoError(t, c.IssueLinkCode(ctx, aliceScope, tg, hash))
	tok, err := c.ClaimLinkCode(ctx, hash, sx.Channel, sx.ExternalID)
	require.NoError(t, err)
	_, err = c.ConfirmMergeByIssuer(ctx, tok.ID, tg.ID)
	require.NoError(t, err)
	_, err = c.MergeOnLink(ctx, tok.ID, sx.Channel, sx.ExternalID)
	assert.ErrorIs(t, err, identity.ErrOtherHousehold)
	u, err := c.UserForAccount(ctx, sx.Channel, sx.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, stranger.ID, u.ID, "a person of another household is never merged")

	// Inside the household.
	hash = identity.HashCode(testCodeKey, "GGGG7777")
	require.NoError(t, c.IssueLinkCode(ctx, aliceScope, tg, hash))
	tok, err = c.ClaimLinkCode(ctx, hash, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	_, err = c.MergeOnLink(ctx, tok.ID, wa.Channel, wa.ExternalID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "the account that typed the code cannot merge before the issuer confirms")
	_, err = c.ConfirmMergeByIssuer(ctx, tok.ID, wa.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "only the issuing account gives the issuer's confirmation")
	_, err = c.ConfirmMergeByIssuer(ctx, tok.ID, tg.ID)
	require.NoError(t, err)
	_, err = c.PendingLink(ctx, tg.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "the issuer has nothing more to confirm")
	_, err = c.ConfirmLink(ctx, tok.ID, tg.ID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "a code the issuer confirmed for a merge cannot be used as a plain link")
	_, err = c.MergeOnLink(ctx, tok.ID, tg.Channel, tg.ExternalID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "the issuer alone cannot merge")
	u, err = c.UserForAccount(ctx, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, alias.ID, u.ID, "nothing is merged on the issuer's confirmation alone")
	pending, err := c.PendingMerge(ctx, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, tok.ID, pending.ID, "the account that typed the code is asked")
	res, err := c.MergeOnLink(ctx, tok.ID, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	_, err = c.MergeOnLink(ctx, tok.ID, wa.Channel, wa.ExternalID)
	assert.ErrorIs(t, err, identity.ErrNothingPending, "a merge runs once")
	assert.Equal(t, alice.ID, res.Survivor)
	assert.EqualValues(t, 1, res.Accounts)

	u, err = c.UserForAccount(ctx, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, alice.ID, u.ID, "the merged account is Alice's")
	_, err = c.GetUser(alias.PlatformID)
	assert.Error(t, err, "the merged person's users row is gone")
	p, err := c.PersonByID(ctx, alias.ID)
	require.NoError(t, err)
	assert.Nil(t, p)

	// The data written under the merged key is Alice's now, read by her key.
	mems, err := repo.ListTopicMemories(ctx, aliceScope, "health", false)
	require.NoError(t, err)
	require.Len(t, mems, 1, "the merged person's personal memory is the survivor's")
	assert.Equal(t, "allergic to penicillin", mems[0].Fact)

	pref, err := c.GetUserPreference(alice.PlatformID)
	require.NoError(t, err)
	assert.Equal(t, "he", pref.PreferredLanguage, "the survivor's preference wins")
	assert.Equal(t, "brief", pref.TonePersona, "an empty field is filled from the merged person's")
	var prefs int64
	require.NoError(t, c.DB.Model(&models.UserPreference{}).Count(&prefs).Error)
	assert.EqualValues(t, 1, prefs, "one preference row is left")
}

// ADR 0002 Decision 4: an account may remove itself; removing another needs an older account; the
// last account cannot be removed.
func TestRemoveAccount_Rules(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", time.Now().Add(-72*time.Hour))
	wa := models.ChannelAccount{PersonID: alice.ID, Channel: "whatsapp", ExternalID: "+15550005005", LinkedAt: time.Now()}
	require.NoError(t, c.DB.Create(&wa).Error)

	assert.ErrorIs(t, c.RemoveAccount(ctx, wa.ID, tg.ID), identity.ErrNotOlder, "a newer account cannot remove an older one")
	_, bob := personWithAccount(t, c, "Levi", "7002", "Bob", "family", "telegram", time.Now().Add(-96*time.Hour))
	assert.ErrorIs(t, c.RemoveAccount(ctx, bob.ID, wa.ID), identity.ErrNoSuchAccount, "nobody removes another person's account")
	require.NoError(t, c.RemoveAccount(ctx, tg.ID, wa.ID), "an older account removes a newer one")
	assert.ErrorIs(t, c.RemoveAccount(ctx, tg.ID, tg.ID), identity.ErrLastAccount)

	wa2 := models.ChannelAccount{PersonID: alice.ID, Channel: "whatsapp", ExternalID: "+15550005006", LinkedAt: time.Now()}
	require.NoError(t, c.DB.Create(&wa2).Error)
	require.NoError(t, c.RemoveAccount(ctx, wa2.ID, wa2.ID), "an account may remove itself")
}

// Messages the assistant starts go to an account the person wrote from: the preferred one, else
// the latest; a person who never wrote gets nothing (V4-32).
func TestDeliveryAccount_PreferredAccountThePersonWroteFrom(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", time.Now().Add(-72*time.Hour))
	wa := models.ChannelAccount{PersonID: alice.ID, Channel: "whatsapp", ExternalID: "+15550006006", LinkedAt: time.Now()}
	require.NoError(t, c.DB.Create(&wa).Error)

	a, err := c.DeliveryAccount(ctx, alice.PlatformID)
	require.NoError(t, err)
	assert.Nil(t, a, "a person who never wrote is not messaged")

	require.NoError(t, c.RecordInbound(ctx, "whatsapp", wa.ExternalID))
	a, err = c.DeliveryAccount(ctx, alice.PlatformID)
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, wa.ID, a.ID, "the account they wrote from")

	require.NoError(t, c.RecordInbound(ctx, "telegram", tg.ExternalID))
	a, err = c.DeliveryAccount(ctx, alice.ID) // users.id names the same person
	require.NoError(t, err)
	assert.Equal(t, tg.ID, a.ID, "the preferred account, once it has written")

	require.NoError(t, c.SetPreferredAccount(ctx, alice.ID, wa.ID))
	a, err = c.DeliveryAccount(ctx, alice.PlatformID)
	require.NoError(t, err)
	assert.Equal(t, wa.ID, a.ID, "notifications follow /notify")

	_, bob := personWithAccount(t, c, "Levi", "7002", "Bob", "family", "telegram", time.Now())
	assert.ErrorIs(t, c.SetPreferredAccount(ctx, alice.ID, bob.ID), identity.ErrNoSuchAccount, "only one's own account can be preferred")

	keys, err := c.AccountKeysOf(ctx, alice.PlatformID)
	require.NoError(t, err)
	assert.Equal(t, []string{wa.ExternalID, tg.ExternalID}, keys, "every account's key, preferred first")
}

// Migration B names no platform: its accounts start without a channel, and the app fills each at
// start-up from the adapter that recognizes the ID. An ID nobody recognizes stays unknown and
// cannot write; a filled channel is never changed again.
func TestResolveAccountChannels_FillsOnlyUnknownChannels(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	h := householdFor(t, c, "Levi")
	for _, id := range []string{"7001", "+15550007007", "odd-id"} {
		require.NoError(t, c.UpsertUser(models.User{PlatformID: id, Name: id, Role: "family", HouseholdID: h.ID, FamilyGroup: "Levi"}))
		u, err := c.GetUser(id)
		require.NoError(t, err)
		require.NoError(t, c.DB.Create(&models.ChannelAccount{PersonID: u.ID, Channel: "", ExternalID: id, LinkedAt: time.Now()}).Error)
	}
	_, known := personWithAccount(t, c, "Levi", "7002", "Known", "family", "telegram", time.Now())

	recognize := func(id string) string {
		switch {
		case strings.HasPrefix(id, "+"):
			return "whatsapp"
		case id != "" && id[0] >= '0' && id[0] <= '9':
			return "telegram"
		}
		return ""
	}
	resolved, unknown, err := c.ResolveAccountChannels(ctx, recognize)
	require.NoError(t, err)
	assert.Equal(t, 2, resolved)
	assert.Equal(t, 1, unknown)

	u, err := c.UserForAccount(ctx, "whatsapp", "+15550007007")
	require.NoError(t, err)
	assert.Equal(t, "+15550007007", u.PlatformID)
	_, err = c.UserForAccount(ctx, "telegram", "7001")
	require.NoError(t, err)
	_, err = c.UserForAccount(ctx, "telegram", "odd-id")
	assert.Error(t, err, "an ID no adapter recognizes cannot write")

	again, _, err := c.ResolveAccountChannels(ctx, func(id string) string {
		if id == "odd-id" {
			return "" // still unknown
		}
		return "other" // would rename the known ones, if anything did
	})
	require.NoError(t, err)
	assert.Zero(t, again, "a known channel is never changed")
	a, err := c.AccountOf(ctx, "telegram", known.ExternalID)
	require.NoError(t, err)
	assert.NotNil(t, a)
}
