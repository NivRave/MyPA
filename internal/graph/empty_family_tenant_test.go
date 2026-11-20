package graph

import (
	"context"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/graph/model"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DE-2: a caller without a family group used to resolve to the shared tenant "default", and two
// resolvers treated "default" as "no filter" — such a caller saw every family's pairing codes and
// mutation logs. The orchestrator scopes the same user to their own ID (CallContext.TenantFamilyGroup);
// GraphQL now does the same, and "all tenants" is an explicit global-admin request only.

func TestResolveTenantAndActor_EmptyFamilyIsPerUser(t *testing.T) {
	// V4-30: the user's own tenant is the household keyed by their user ID (the migration's rule).
	r := &Resolver{Households: labelStore("solo-1", "default", "MyFamily", "solo-2")}
	ctx := WithClaims(context.Background(), &AuthClaims{UserID: "solo-1", Role: "member"})

	_, hs, err := r.resolveTenantAndActor(ctx, nil)
	fg := hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "solo-1", fg, "no family group → the user's own tenant, like the orchestrator")

	own := "solo-1"
	_, hs, err = r.resolveTenantAndActor(ctx, &own)
	fg = hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "solo-1", fg)

	for _, other := range []string{"default", "MyFamily", "solo-2"} {
		_, _, err = r.resolveTenantAndActor(ctx, &other)
		assert.Error(t, err, "a caller without a family group must not reach tenant %q", other)
	}
}

func TestResolveTenantAndActor_GlobalAdminWithoutFamilyIsPerUser(t *testing.T) {
	r := &Resolver{Households: labelStore("root")}
	ctx := WithClaims(context.Background(), &AuthClaims{UserID: "root", Role: "admin"})
	_, hs, err := r.resolveTenantAndActor(ctx, nil)
	fg := hs.LegacyLabel()
	require.NoError(t, err)
	assert.Equal(t, "root", fg, "writes without a target land in the admin's own tenant, never a shared one")
}

// Without a user ID the per-user fallback would put every such caller into one tenant ("user").
func TestResolveTenantAndActor_NoIDNoFamilyHasNoTenant(t *testing.T) {
	r := &Resolver{Households: labelStore("user")}
	for _, role := range []string{"member", "admin"} {
		_, _, err := r.resolveTenantAndActor(WithClaims(context.Background(), &AuthClaims{Role: role}), nil)
		assert.Error(t, err, "role %s without user id and family group must be refused", role)
	}
}

// "*" means "all tenants" on list queries only; nobody may read from or write into a tenant "*".
func TestResolveTenantAndActor_StarIsNotATenant(t *testing.T) {
	r := &Resolver{Households: labelStore("root", "f", "*")}
	star := "*"
	for _, c := range []*AuthClaims{
		{UserID: "root", Role: "admin"},
		{UserID: "m", Role: "member", FamilyGroup: "f"},
	} {
		_, _, err := r.resolveTenantAndActor(WithClaims(context.Background(), c), &star)
		assert.Error(t, err)
	}
	assert.True(t, wantsAllTenants(&AuthClaims{Role: "admin"}, &star))
	assert.False(t, wantsAllTenants(&AuthClaims{Role: "admin", TenantBound: true}, &star), "the dashboard secret is never cross-tenant")
	assert.False(t, wantsAllTenants(&AuthClaims{Role: "member"}, nil))
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
	solo := WithClaims(context.Background(), &AuthClaims{UserID: soloID, Role: "member"})

	codes, err := resolver.Query().ActivePairingCodes(solo, nil)
	require.NoError(t, err)
	for _, c := range codes {
		assert.NotEqual(t, victimFG, c.FamilyGroup, "a caller without a family group must not list another family's pairing codes")
	}

	logs, err := resolver.Query().MutationLogs(solo, nil, nil, nil)
	require.NoError(t, err)
	for _, l := range logs {
		assert.NotEqual(t, victimFG, l.FamilyGroup, "a caller without a family group must not read another family's mutation logs")
	}

	// A global admin still gets the cross-tenant view — but only by asking for it.
	admin := WithClaims(context.Background(), &AuthClaims{UserID: "root", Role: "admin"})
	for _, scope := range []*string{nil, strPtr("*")} {
		codes, err = resolver.Query().ActivePairingCodes(admin, scope)
		require.NoError(t, err)
		assert.True(t, containsCode(codes, code.Code), "global admin unscoped view lists every family's codes")

		logs, err = resolver.Query().MutationLogs(admin, scope, nil, nil)
		require.NoError(t, err)
		assert.True(t, containsLog(logs, logRow.ID), "global admin unscoped view lists every family's logs")
	}

	// A tenant-bound admin (the dashboard) stays inside its family.
	dashFG := "fam-dash-" + uuid.New().String()
	households(t, resolver.DB, dashFG)
	dash := WithClaims(context.Background(), &AuthClaims{UserID: "dashboard-service", Role: "admin", FamilyGroup: dashFG, TenantBound: true})
	codes, err = resolver.Query().ActivePairingCodes(dash, nil)
	require.NoError(t, err)
	assert.False(t, containsCode(codes, code.Code))
	logs, err = resolver.Query().MutationLogs(dash, nil, nil, nil)
	require.NoError(t, err)
	assert.False(t, containsLog(logs, logRow.ID))
}

func TestSaveUserPreference_EventCarriesCallerTenant(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-pref-" + uuid.New().String()
	user := "usr-pref-" + uuid.New().String()
	households(t, resolver.DB, fam)
	ctx := WithClaims(context.Background(), &AuthClaims{UserID: user, Role: "member", FamilyGroup: fam})

	lang := "he"
	_, err := resolver.Mutation().SaveUserPreference(ctx, user, &lang, nil, nil, nil, nil, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(tracker.getEvents()) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, fam, tracker.getEvents()[0].FamilyGroup, "the preference change must be logged in the caller's family, not a shared tenant")
}

func strPtr(s string) *string { return &s }

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
