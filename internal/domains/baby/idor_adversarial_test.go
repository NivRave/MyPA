package baby_test

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSEC04_BabyDeclarations_NoTenantOverrides verifies tool declarations do not accept family_group overrides.
func TestSEC04_BabyDeclarations_NoTenantOverrides(t *testing.T) {
	mod := baby.NewModule(newMockBabyRepo())
	for _, decl := range mod.Declarations() {
		if decl.Parameters != nil && decl.Parameters.Properties != nil {
			assert.Nil(t, decl.Parameters.Properties["family_group"], "tool %s should not declare family_group parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["family_id"], "tool %s should not declare family_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["user_id"], "tool %s should not declare user_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["fgOverride"], "tool %s should not declare fgOverride parameter", decl.Name)
		}
	}
}

// TestSEC04_CrossTenant_BabyProfileAndCareLogs_Isolation tests cross-tenant IDOR protections.
func TestSEC04_CrossTenant_BabyProfileAndCareLogs_Isolation(t *testing.T) {
	repo := newMockBabyRepo()
	mod := baby.NewModule(repo)
	ctx := context.Background()

	// 1. Family Alpha creates baby profile "Leo"
	userAlpha := &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := orchestrator.CallContext{User: userAlpha, Message: models.Message{UserID: "user_alpha"}}

	resp, err := mod.Execute(ctx, callCtxAlpha, "upsert_baby_profile", map[string]interface{}{
		"name":          "Leo",
		"date_of_birth": "2026-06-15",
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "Leo")

	// Verify profile in repo
	alphaProfiles, err := repo.GetBabyProfilesForFamily("family_alpha")
	require.NoError(t, err)
	require.Len(t, alphaProfiles, 1)
	leoID := alphaProfiles[0].ID

	// 2. Family Alpha logs care
	_, err = mod.Execute(ctx, callCtxAlpha, "log_baby_care", map[string]interface{}{
		"baby_id":       int(leoID),
		"log_type":      "feeding",
		"value_numeric": 120,
		"unit":          "ml",
	})
	require.NoError(t, err)

	// 3. Family Beta attempts IDOR attacks
	userBeta := &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := orchestrator.CallContext{User: userBeta, Message: models.Message{UserID: "user_beta"}}

	// Attack A: Try to update Leo's profile using his baby_id
	_, err = mod.Execute(ctx, callCtxBeta, "upsert_baby_profile", map[string]interface{}{
		"id":            int(leoID),
		"name":          "Leo Hijacked",
		"date_of_birth": "2026-06-15",
	})
	assert.Error(t, err, "Family Beta must not be able to update Family Alpha's baby profile")
	assert.Contains(t, err.Error(), "not found in family")

	// Attack B: Try to log care for Leo
	_, err = mod.Execute(ctx, callCtxBeta, "log_baby_care", map[string]interface{}{
		"baby_id":       int(leoID),
		"log_type":      "feeding",
		"value_numeric": 999,
		"unit":          "ml",
	})
	assert.Error(t, err, "Family Beta must not be able to log care for Family Alpha's baby")
	assert.Contains(t, err.Error(), "not found in family")

	// Attack C: Try to query care logs for Leo
	queryResp, err := mod.Execute(ctx, callCtxBeta, "query_baby_logs_and_trends", map[string]interface{}{
		"baby_id": int(leoID),
	})
	require.NoError(t, err)
	t.Logf("Family Beta query logs response: %s", queryResp)
	assert.NotContains(t, queryResp, "120", "Family Beta must NOT see Family Alpha's 120ml feeding log")

	// Attack D: Try to get milestone suggestions for Leo
	_, err = mod.Execute(ctx, callCtxBeta, "get_baby_milestone_suggestions", map[string]interface{}{
		"baby_id": int(leoID),
	})
	assert.Error(t, err, "Family Beta must not query milestones for Family Alpha's baby")

	// 4. Test users with empty family_group
	userSolo1 := &models.User{ID: "u_solo1", PlatformID: "solo_1", FamilyGroup: ""}
	callCtxSolo1 := orchestrator.CallContext{User: userSolo1, Message: models.Message{UserID: "solo_1"}}

	_, err = mod.Execute(ctx, callCtxSolo1, "upsert_baby_profile", map[string]interface{}{
		"name":          "SoloBaby",
		"date_of_birth": "2026-08-01",
	})
	require.NoError(t, err)

	solo1Profiles, err := repo.GetBabyProfilesForFamily("solo_1")
	require.NoError(t, err)
	require.Len(t, solo1Profiles, 1)
	soloBabyID := solo1Profiles[0].ID

	userSolo2 := &models.User{ID: "u_solo2", PlatformID: "solo_2", FamilyGroup: ""}
	callCtxSolo2 := orchestrator.CallContext{User: userSolo2, Message: models.Message{UserID: "solo_2"}}

	// Solo 2 attempts to query Solo 1's baby
	solo2Resp, err := mod.Execute(ctx, callCtxSolo2, "query_baby_logs_and_trends", map[string]interface{}{
		"baby_id": int(soloBabyID),
	})
	require.NoError(t, err)
	t.Logf("Solo 2 query logs response: %s", solo2Resp)
	assert.NotContains(t, solo2Resp, "SoloBaby", "Solo 2 must NOT see Solo 1's baby")

	// Solo 2 attempts to log care for Solo 1's baby
	_, err = mod.Execute(ctx, callCtxSolo2, "log_baby_care", map[string]interface{}{
		"baby_id":       int(soloBabyID),
		"log_type":      "sleep",
		"value_numeric": 2,
		"unit":          "hours",
	})
	assert.Error(t, err, "Solo 2 must not be able to log care for Solo 1's baby")
	_ = queryResp
}
