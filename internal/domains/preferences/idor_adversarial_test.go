package preferences

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idorScope is the scope of u's household: its label, or its own platform ID without one.
func idorScope(t *testing.T, u *models.User) identity.Scope {
	t.Helper()
	label := u.FamilyGroup
	if label == "" {
		label = u.PlatformID
	}
	return identitytest.Scope(t, label, u.PlatformID, "family")
}

// TestSEC04_PreferencesDeclarations_NoTenantOverrides verifies tool declarations do not expose
// tenant overrides (moved with the tools from the automation module, V4-27).
func TestSEC04_PreferencesDeclarations_NoTenantOverrides(t *testing.T) {
	mod := NewModule(newFakePreferencesRepo())
	for _, decl := range mod.Declarations() {
		if decl.Parameters != nil && decl.Parameters.Properties != nil {
			assert.Nil(t, decl.Parameters.Properties["family_group"], "tool %s should not declare family_group parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["family_id"], "tool %s should not declare family_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["user_id"], "tool %s should not declare user_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["fgOverride"], "tool %s should not declare fgOverride parameter", decl.Name)
		}
	}
}

// TestSEC04_CrossTenant_PersonalCRM_Overwrite_Blocked empirically verifies that
// CRM contacts cannot be overwritten across tenants via ID in tool arguments,
// and verifies non-regression on legitimate same-family updates.
func TestSEC04_CrossTenant_PersonalCRM_Overwrite_Blocked(t *testing.T) {
	repo := newFakePreferencesRepo()
	mod := NewModule(repo)
	ctx := context.Background()

	// 1. Family Alpha creates a CRM contact
	userAlpha := &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := orchestrator.CallContext{User: userAlpha, Message: channel.Inbound{Sender: "user_alpha"}, Scope: idorScope(t, userAlpha)}

	respAlpha, err := mod.Execute(ctx, callCtxAlpha, "upsert_personal_crm", map[string]interface{}{
		"person_name":  "Grandma Alice",
		"relationship": "grandmother",
	})
	require.NoError(t, err)
	assert.Contains(t, respAlpha, "Grandma Alice")

	// Verify Contact 1 exists
	contact1, err := repo.GetCRMContact(callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	require.NotNil(t, contact1)
	assert.Equal(t, "Grandma Alice", contact1.PersonName)
	assert.Equal(t, "family_alpha", contact1.FamilyGroup)

	// 2. Family Beta attempts to overwrite Family Alpha's contact via ID: 1
	userBeta := &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := orchestrator.CallContext{User: userBeta, Message: channel.Inbound{Sender: "user_beta"}, Scope: idorScope(t, userBeta)}

	_, errBeta := mod.Execute(ctx, callCtxBeta, "upsert_personal_crm", map[string]interface{}{
		"id":           1,
		"person_name":  "Hacked Grandma",
		"relationship": "attacker",
	})
	assert.Error(t, errBeta, "Family Beta must NOT be able to overwrite Family Alpha's CRM contact")
	assert.Contains(t, errBeta.Error(), "permission denied")

	// Ensure Contact 1 was NOT altered
	contact1After, err := repo.GetCRMContact(callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	assert.Equal(t, "Grandma Alice", contact1After.PersonName)
	assert.Equal(t, "grandmother", contact1After.Relationship)

	// 3. Legitimate same-family update (non-regression)
	respAlphaUpdate, err := mod.Execute(ctx, callCtxAlpha, "upsert_personal_crm", map[string]interface{}{
		"id":           1,
		"person_name":  "Grandma Alice Smith",
		"relationship": "grandmother",
	})
	require.NoError(t, err, "Family Alpha must be able to update their own contact")
	assert.Contains(t, respAlphaUpdate, "Grandma Alice Smith")

	contact1Updated, err := repo.GetCRMContact(callCtxAlpha.Scope, 1)
	require.NoError(t, err)
	assert.Equal(t, "Grandma Alice Smith", contact1Updated.PersonName)

	// 4. Solo user isolation (empty FamilyGroup)
	userSolo1 := &models.User{ID: "u_solo1", PlatformID: "solo1", FamilyGroup: ""}
	callCtxSolo1 := orchestrator.CallContext{User: userSolo1, Message: channel.Inbound{Sender: "solo1"}, Scope: idorScope(t, userSolo1)}
	_, err = mod.Execute(ctx, callCtxSolo1, "upsert_personal_crm", map[string]interface{}{
		"person_name": "Solo Contact",
	})
	require.NoError(t, err)

	userSolo2 := &models.User{ID: "u_solo2", PlatformID: "solo2", FamilyGroup: ""}
	callCtxSolo2 := orchestrator.CallContext{User: userSolo2, Message: channel.Inbound{Sender: "solo2"}, Scope: idorScope(t, userSolo2)}
	_, errSolo2 := mod.Execute(ctx, callCtxSolo2, "upsert_personal_crm", map[string]interface{}{
		"id":          2,
		"person_name": "Stolen Contact",
	})
	assert.Error(t, errSolo2, "Solo2 must NOT be able to overwrite Solo1's contact")
	assert.Contains(t, errSolo2.Error(), "permission denied")
}
