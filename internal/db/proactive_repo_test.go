package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDelivery(personID, state string, now time.Time) *models.ProactiveDelivery {
	return &models.ProactiveDelivery{PersonID: personID, Category: "reminder", Text: "Call the dentist", State: state,
		TemplateValues: `["Call the dentist"]`, DueAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now}
}

// The Postgres store of V4-33 does what the policy relies on: one claim wins, a report finds its
// row by any part's ID, and held rows expire, then are purged a month after they end.
func TestProactiveStore_RoundTrip(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	alice, _ := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", time.Now())
	now := time.Now().UTC().Truncate(time.Second)

	held := newDelivery(alice.ID, models.DeliveryHeld, now)
	require.NoError(t, c.CreateDelivery(ctx, held))
	require.NotEmpty(t, held.ID)
	got, err := c.HeldDeliveries(ctx, alice.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Call the dentist", got[0].Text)

	// Exactly one of many concurrent claims wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := c.ClaimDelivery(ctx, held.ID, []string{models.DeliveryHeld})
			assert.NoError(t, err)
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, wins)

	// A sent row is found by the ID of any of its parts, on its channel only.
	held.State, held.Channel, held.Refs = models.DeliveryDelivered, "whatsapp", "SM1,SM2"
	require.NoError(t, c.SaveDelivery(ctx, held))
	byRef, err := c.DeliveryByRef(ctx, "whatsapp", "SM2")
	require.NoError(t, err)
	require.NotNil(t, byRef)
	assert.Equal(t, held.ID, byRef.ID)
	for _, miss := range [][2]string{{"whatsapp", "SM"}, {"telegram", "SM1"}, {"whatsapp", ""}} {
		none, err := c.DeliveryByRef(ctx, miss[0], miss[1])
		require.NoError(t, err)
		assert.Nil(t, none, "%v", miss)
	}

	// Pending rows are due at their time.
	next := now.Add(time.Minute)
	pending := newDelivery(alice.ID, models.DeliveryPending, now)
	pending.NextAttemptAt = &next
	require.NoError(t, c.CreateDelivery(ctx, pending))
	due, err := c.DueDeliveries(ctx, now)
	require.NoError(t, err)
	assert.Empty(t, due)
	due, err = c.DueDeliveries(ctx, next)
	require.NoError(t, err)
	require.Len(t, due, 1)

	// Counts for /doctor, through the household's memberships.
	h := householdFor(t, c, "Levi")
	waiting, expired, err := c.DeliveryCounts(ctx, h.ID, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, waiting)
	assert.EqualValues(t, 0, expired)
	other := householdFor(t, c, "Cohen")
	waiting, _, err = c.DeliveryCounts(ctx, other.ID, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Zero(t, waiting, "another household's messages are not counted")

	// Expiry ends held and pending rows only.
	ended, err := c.ExpireDeliveries(ctx, now.Add(8*24*time.Hour))
	require.NoError(t, err)
	require.Len(t, ended, 1)
	assert.Equal(t, pending.ID, ended[0].ID)
	assert.Equal(t, models.DeliveryExpired, ended[0].State)
	_, expired, err = c.DeliveryCounts(ctx, h.ID, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, expired)

	// A row left sending is made pending again.
	stuck := newDelivery(alice.ID, models.DeliverySending, now)
	stuck.UpdatedAt = now.Add(-10 * time.Minute)
	require.NoError(t, c.DB.Create(stuck).Error)
	n, err := c.UnstickDeliveries(ctx, now.Add(-5*time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	// Ended rows are purged after the time; open ones are kept.
	n, err = c.PurgeDeliveries(ctx, now.Add(9*24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 2, n, "the delivered and the expired row")
	var left int64
	require.NoError(t, c.DB.Model(&models.ProactiveDelivery{}).Count(&left).Error)
	assert.EqualValues(t, 1, left, "the row made pending again")
}

// Merging two people keeps the merged one's messages still to deliver: they follow the survivor
// instead of being dropped with the merged person (V4-32 with V4-33).
func TestMergeOnLink_HeldMessagesFollowTheSurvivor(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)
	alice, tg := personWithAccount(t, c, "Levi", "7001", "Alice", "family", "telegram", old)
	alias, wa := personWithAccount(t, c, "Levi", "+15550003003", "Alice WhatsApp", "family", "whatsapp", old)
	held := newDelivery(alias.ID, models.DeliveryHeld, time.Now())
	require.NoError(t, c.CreateDelivery(ctx, held))

	hash := identity.HashCode(testCodeKey, "HHHH8888")
	require.NoError(t, c.IssueLinkCode(ctx, scopeOfUser(t, c, alice), tg, hash))
	tok, err := c.ClaimLinkCode(ctx, hash, wa.Channel, wa.ExternalID)
	require.NoError(t, err)
	_, err = c.ConfirmMergeByIssuer(ctx, tok.ID, tg.ID)
	require.NoError(t, err)
	_, err = c.MergeOnLink(ctx, tok.ID, wa.Channel, wa.ExternalID)
	require.NoError(t, err)

	got, err := c.HeldDeliveries(ctx, alice.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, held.ID, got[0].ID)
}
