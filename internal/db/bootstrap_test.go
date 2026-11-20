package db

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR 0002 Decision 6 (option 6b): ALLOWED_USERS seeds a fresh install once — one household per
// label, a household of its own for an entry without one, the first entry made admin where a
// household has none — and is ignored as soon as a household exists.
func TestBootstrapHouseholds_SeedsOnceThenTheDatabaseDecides(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	entries := []models.User{
		{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"},
		{PlatformID: "1002", Name: "Omer", Role: "family", FamilyGroup: " Levi "},
		{PlatformID: "2001", Name: "Rina", Role: "family", FamilyGroup: "Cohen"},
		{PlatformID: "2002", Name: "Avi", Role: "family", FamilyGroup: "Cohen"},
		{PlatformID: "3001", Name: "Solo", Role: "family"},
	}
	res, err := client.BootstrapHouseholds(ctx, entries, "Asia/Jerusalem", telegramIDs)
	require.NoError(t, err)
	assert.False(t, res.Ignored)
	assert.Equal(t, 3, res.Households)
	assert.Equal(t, 5, res.Users)
	assert.Equal(t, []string{"2001", "3001"}, res.Promoted, "a household without an admin gets its first entry as admin")

	n, err := client.CountHouseholds(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)

	get := func(id string) *models.User {
		u, err := client.GetUser(id)
		require.NoError(t, err)
		return u
	}
	dana, omer, rina, avi, solo := get("1001"), get("1002"), get("2001"), get("2002"), get("3001")
	assert.Equal(t, dana.HouseholdID, omer.HouseholdID, "the same label, trimmed, is one household")
	assert.Equal(t, rina.HouseholdID, avi.HouseholdID)
	assert.NotEqual(t, dana.HouseholdID, rina.HouseholdID)
	assert.NotEqual(t, solo.HouseholdID, dana.HouseholdID)
	assert.Equal(t, "admin", dana.Role)
	assert.Equal(t, "family", omer.Role)
	assert.Equal(t, "admin", rina.Role, "promoted: Cohen had no admin")
	assert.Equal(t, "family", avi.Role)
	assert.Equal(t, "admin", solo.Role, "an entry alone in its household owns it")
	assert.Equal(t, "Levi", omer.FamilyGroup, "the legacy label is written beside the household")
	assert.Equal(t, "3001", solo.FamilyGroup, "an unlabelled entry's legacy key is its platform ID, as before V4-30")

	h, err := client.HouseholdByID(ctx, solo.HouseholdID)
	require.NoError(t, err)
	assert.Equal(t, "Solo", h.Name)
	assert.Equal(t, "Asia/Jerusalem", h.TimeZone, "a new household starts with the install's time zone")

	// A role changed in the database survives a restart: the setting is not read again.
	require.NoError(t, client.DB.Model(&models.User{}).Where("platform_id = ?", "1002").Update("role", "admin").Error)
	changed := append(entries, models.User{PlatformID: "9009", Name: "Late", Role: "admin", FamilyGroup: "Levi"})
	changed[1].Role = "family"
	res, err = client.BootstrapHouseholds(ctx, changed, "UTC", telegramIDs)
	require.NoError(t, err)
	assert.True(t, res.Ignored)
	assert.Zero(t, res.Households)
	assert.Equal(t, "admin", get("1002").Role, "the database is the single truth after the first start")
	_, err = client.GetUser("9009")
	assert.Error(t, err, "editing the setting after the first start adds nobody")
}

// An empty setting on a fresh install creates nothing.
func TestBootstrapHouseholds_EmptySettingCreatesNothing(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	res, err := client.BootstrapHouseholds(context.Background(), nil, "UTC", telegramIDs)
	require.NoError(t, err)
	assert.False(t, res.Ignored)
	assert.Zero(t, res.Households)
	n, err := client.CountHouseholds(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
}

// telegramIDs names the channel of a test platform ID: a leading '+' is WhatsApp, a "9" prefix is
// unknown, anything else Telegram.
func telegramIDs(id string) string {
	switch {
	case len(id) > 0 && id[0] == '+':
		return "whatsapp"
	case len(id) > 0 && id[0] == '9':
		return ""
	default:
		return "telegram"
	}
}

// V4-32: every entry the bootstrap creates is a person (keeping the user's ID), a member of its
// household and the owner of one channel account, named by the entry's channel; an entry whose
// channel is unknown is counted and gets no account.
func TestBootstrapHouseholds_EveryEntryIsAPersonWithAnAccount(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	entries := []models.User{
		{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"},
		{PlatformID: "+15550001002", Name: "Omer", Role: "family", FamilyGroup: "Levi"},
		{PlatformID: "9003", Name: "Unknown channel", Role: "family", FamilyGroup: "Levi"},
	}
	res, err := client.BootstrapHouseholds(ctx, entries, "UTC", telegramIDs)
	require.NoError(t, err)
	assert.Equal(t, 3, res.Users)
	assert.Equal(t, 1, res.NoChannel)

	dana, err := client.UserForAccount(ctx, "telegram", "1001")
	require.NoError(t, err)
	assert.Equal(t, "Dana", dana.Name)
	omer, err := client.UserForAccount(ctx, "whatsapp", "+15550001002")
	require.NoError(t, err)
	assert.Equal(t, dana.HouseholdID, omer.HouseholdID)

	for _, u := range []*models.User{dana, omer} {
		p, err := client.PersonByID(ctx, u.ID)
		require.NoError(t, err)
		require.NotNil(t, p, "the person keeps the user's ID")
		require.NotNil(t, p.PreferredAccountID, "the one account is the preferred one")
		var m models.Membership
		require.NoError(t, client.DB.Where("person_id = ?", u.ID).First(&m).Error)
		assert.Equal(t, u.HouseholdID, m.HouseholdID)
	}
	nobody, err := client.UserForAccount(ctx, "telegram", "9003")
	assert.Error(t, err, "an entry with an unknown channel cannot write until it has an account")
	assert.Nil(t, nobody)
}
