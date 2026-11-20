package graph

import (
	"context"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/graph/model"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DE-2: a caller without a family group used to resolve to the shared tenant "default", and two
// resolvers treated "default" as "no filter" — such a caller saw every family's pairing codes and
// mutation logs. The orchestrator scopes the same user to their own ID; GraphQL did the same, and
// since V4-35 no caller can name a household at all: the signed-in person's household comes from
// their membership, and no label ("default", "*") means anything but the household that has it.

func TestCaller_EmptyFamilyIsPerUser(t *testing.T) {
	// V4-30: the user's own tenant is the household keyed by their user ID (the migration's rule).
	r := &Resolver{Households: labelStore("solo-1", "default", "MyFamily", "solo-2")}
	_, hs, err := r.caller(signedIn(t, r, "", "solo-1", "member"))
	require.NoError(t, err)
	assert.Equal(t, "solo-1", hs.LegacyLabel(), "no family group → the user's own tenant, like the orchestrator")
	for _, other := range []string{"default", "MyFamily", "solo-2"} {
		assert.NotEqual(t, identitytest.HouseholdID(other), hs.HouseholdID(), "a caller without a family group must not reach tenant %q", other)
	}
}

func TestCaller_OwnerWithoutFamilyIsPerUser(t *testing.T) {
	r := &Resolver{Households: labelStore("root")}
	_, hs, err := r.caller(signedIn(t, r, "", "root", "admin"))
	require.NoError(t, err)
	assert.Equal(t, "root", hs.LegacyLabel(), "writes without a target land in the person's own tenant, never a shared one")
}

// Without a household there is no caller: a zero scope cannot act.
func TestCaller_NoIDNoFamilyHasNoTenant(t *testing.T) {
	r := &Resolver{Households: labelStore("user")}
	_, _, err := r.caller(WithCaller(context.Background(), identity.Scope{}))
	assert.Error(t, err, "a caller without a household must be refused")
}

// "*" used to mean "all tenants" on list queries; now a household labelled "*" is only itself.
func TestCaller_StarIsNotATenant(t *testing.T) {
	r := &Resolver{Households: labelStore("root", "f", "*")}
	_, hs, err := r.caller(signedIn(t, r, "*", "m", "admin"))
	require.NoError(t, err)
	assert.Equal(t, identitytest.HouseholdID("*"), hs.HouseholdID())
	assert.NotEqual(t, identitytest.HouseholdID("f"), hs.HouseholdID())
}

func TestEmptyFamilyCaller_SeesNoOtherFamilysPairingCodesOrLogs(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()

	victimFG := "fam-victim-" + uuid.New().String()
	victim := householdtest.Household(t, resolver.DB, victimFG)
	code := models.PairingCode{Code: "PAIR-" + uuid.New().String()[:8], Role: "member", HouseholdID: victim.ID, FamilyGroup: victimFG, ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, resolver.DB.DB.Create(&code).Error)
	logRow := models.MutationLog{ID: uuid.New().String(), UserID: "victim-user", HouseholdID: victim.ID, FamilyGroup: victimFG, Topic: "baby", Action: "create",
		EntityType: "baby_profile", EntityID: "1", BeforeJSON: "{}", AfterJSON: `{"name":"secret"}`, CreatedAt: time.Now()}
	require.NoError(t, resolver.DB.DB.Create(&logRow).Error)

	soloID := "solo-" + uuid.New().String()
	households(t, resolver.DB, soloID) // an unlabelled user's own household, keyed by their ID
	// The pairing codes are an owner's to list (V4-35): the solo caller owns their own household.
	solo := signedIn(t, resolver, "", soloID, "admin")

	codes, err := resolver.Query().ActivePairingCodes(solo)
	require.NoError(t, err)
	for _, c := range codes {
		assert.NotEqual(t, victimFG, c.FamilyGroup, "a caller without a family group must not list another family's pairing codes")
	}

	logs, err := resolver.Query().MutationLogs(solo, nil, nil)
	require.NoError(t, err)
	for _, l := range logs {
		assert.NotEqual(t, victimFG, l.FamilyGroup, "a caller without a family group must not read another family's mutation logs")
	}

	// V4-35: the global admin's cross-tenant view is gone. The install's operator, signed in, sees
	// their own household only (ADR 0002 question 2: the operator does not read household content).
	admin := signedIn(t, resolver, "", "root", "admin")
	codes, err = resolver.Query().ActivePairingCodes(admin)
	require.NoError(t, err)
	assert.False(t, containsCode(codes, code.Code), "no caller lists every family's codes")
	logs, err = resolver.Query().MutationLogs(admin, nil, nil)
	require.NoError(t, err)
	assert.False(t, containsLog(logs, logRow.ID), "no caller lists every family's logs")

	// An owner of another household (the dashboard before V4-35) stays inside it.
	dashFG := "fam-dash-" + uuid.New().String()
	households(t, resolver.DB, dashFG)
	dash := signedIn(t, resolver, dashFG, "dashboard-service", "admin")
	codes, err = resolver.Query().ActivePairingCodes(dash)
	require.NoError(t, err)
	assert.False(t, containsCode(codes, code.Code))
	logs, err = resolver.Query().MutationLogs(dash, nil, nil)
	require.NoError(t, err)
	assert.False(t, containsLog(logs, logRow.ID))
}

func TestSaveUserPreference_EventCarriesCallerTenant(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-pref-" + uuid.New().String()
	user := "usr-pref-" + uuid.New().String()
	households(t, resolver.DB, fam)
	ctx := signedIn(t, resolver, fam, user, "member")

	lang := "he"
	_, err := resolver.Mutation().SaveUserPreference(ctx, user, &lang, nil, nil, nil, nil, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(tracker.getEvents()) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, fam, tracker.getEvents()[0].FamilyGroup, "the preference change must be logged in the caller's family, not a shared tenant")
}

func containsCode(codes []*model.PairingCode, code string) bool {
	for _, c := range codes {
		if c.Code == code {
			return true
		}
	}
	return false
}

func containsLog(logs []*model.MutationLog, id string) bool {
	for _, l := range logs {
		if l.ID == id {
			return true
		}
	}
	return false
}
