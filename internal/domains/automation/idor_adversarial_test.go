package automation_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type idorAutomationRepo struct {
	mu          sync.RWMutex
	connectors  map[uint]*models.ExternalAPIConnector
	rules       map[uint]*models.AutomationRule
	preferences map[string]*models.UserPreference
	contacts    map[uint]*models.PersonalCRMContact
	nextConnID  uint
	nextRuleID  uint
	nextCrmID   uint
}

func newIDORAutomationRepo() *idorAutomationRepo {
	return &idorAutomationRepo{
		connectors:  make(map[uint]*models.ExternalAPIConnector),
		rules:       make(map[uint]*models.AutomationRule),
		preferences: make(map[string]*models.UserPreference),
		contacts:    make(map[uint]*models.PersonalCRMContact),
		nextConnID:  1,
		nextRuleID:  1,
		nextCrmID:   1,
	}
}

func (r *idorAutomationRepo) CreateAPIConnector(c *models.ExternalAPIConnector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.ID = r.nextConnID
	r.nextConnID++
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	cp := *c
	r.connectors[c.ID] = &cp
	return nil
}
func (r *idorAutomationRepo) GetAPIConnector(id uint, familyGroup ...string) (*models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (r *idorAutomationRepo) GetAPIConnectorByName(userID, familyGroup, name string) (*models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	normName := strings.ToLower(strings.TrimSpace(name))
	for _, c := range r.connectors {
		if strings.ToLower(c.Name) == normName {
			if (familyGroup != "" && c.FamilyGroup == familyGroup) || (userID != "" && c.UserID == userID) {
				cp := *c
				return &cp, nil
			}
		}
	}
	return nil, nil
}
func (r *idorAutomationRepo) ListAPIConnectors(userID, familyGroup string) ([]models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.ExternalAPIConnector
	for _, c := range r.connectors {
		if (familyGroup != "" && c.FamilyGroup == familyGroup) || (userID != "" && c.UserID == userID) {
			list = append(list, *c)
		}
	}
	return list, nil
}
func (r *idorAutomationRepo) DeleteAPIConnector(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.connectors, id)
	return nil
}
func (r *idorAutomationRepo) CreateAutomationRule(rule *models.AutomationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.ID = r.nextRuleID
	r.nextRuleID++
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}
func (r *idorAutomationRepo) GetAutomationRule(id uint) (*models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[id]
	if !ok {
		return nil, nil
	}
	cp := *rule
	return &cp, nil
}
func (r *idorAutomationRepo) ListAutomationRules(userID, familyGroup, triggerTopic string) ([]models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.AutomationRule
	normTopic := strings.ToLower(strings.TrimSpace(triggerTopic))
	for _, rule := range r.rules {
		if (familyGroup != "" && rule.FamilyGroup == familyGroup) || (userID != "" && rule.UserID == userID) {
			if normTopic == "" || normTopic == "all" || normTopic == "*" || strings.ToLower(rule.TriggerTopic) == normTopic {
				list = append(list, *rule)
			}
		}
	}
	return list, nil
}
func (r *idorAutomationRepo) ListActiveAutomationRules(triggerTopic string) ([]models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.AutomationRule
	for _, rule := range r.rules {
		if rule.IsActive {
			list = append(list, *rule)
		}
	}
	return list, nil
}
func (r *idorAutomationRepo) UpdateAutomationRule(rule *models.AutomationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}
func (r *idorAutomationRepo) DeleteAutomationRule(id uint, familyGroup ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}
func (r *idorAutomationRepo) UpsertUserPreference(pref *models.UserPreference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *pref
	r.preferences[pref.UserID] = &cp
	return nil
}
func (r *idorAutomationRepo) GetUserPreference(userID string) (*models.UserPreference, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pref, ok := r.preferences[userID]
	if !ok {
		return nil, nil
	}
	cp := *pref
	return &cp, nil
}
func (r *idorAutomationRepo) UpsertCRMContact(contact *models.PersonalCRMContact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if contact.ID == 0 {
		contact.ID = r.nextCrmID
		r.nextCrmID++
	}
	if contact.UpdatedAt.IsZero() {
		contact.UpdatedAt = time.Now()
	}
	cp := *contact
	r.contacts[contact.ID] = &cp
	return nil
}
func (r *idorAutomationRepo) GetCRMContact(id uint) (*models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.contacts[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (r *idorAutomationRepo) ListCRMContacts(userID, familyGroup, query string) ([]models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.PersonalCRMContact
	for _, c := range r.contacts {
		if (familyGroup != "" && c.FamilyGroup == familyGroup) || (userID != "" && c.UserID == userID) {
			list = append(list, *c)
		}
	}
	return list, nil
}
func (r *idorAutomationRepo) DeleteCRMContact(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.contacts, id)
	return nil
}

// TestSEC04_AutomationDeclarations_NoTenantOverrides verifies tool declarations do not expose tenant overrides.
func TestSEC04_AutomationDeclarations_NoTenantOverrides(t *testing.T) {
	mod := automation.NewModule(newIDORAutomationRepo())
	for _, decl := range mod.Declarations() {
		if decl.Parameters != nil && decl.Parameters.Properties != nil {
			assert.Nil(t, decl.Parameters.Properties["family_group"], "tool %s should not declare family_group parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["family_id"], "tool %s should not declare family_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["user_id"], "tool %s should not declare user_id parameter", decl.Name)
			assert.Nil(t, decl.Parameters.Properties["fgOverride"], "tool %s should not declare fgOverride parameter", decl.Name)
		}
	}
}

// TestSEC04_CrossTenant_AutomationRules_Isolation verifies rules cannot be deleted across family groups.
func TestSEC04_CrossTenant_AutomationRules_Isolation(t *testing.T) {
	repo := newIDORAutomationRepo()
	mod := automation.NewModule(repo)
	ctx := context.Background()

	// Family Alpha creates rule
	userAlpha := &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := orchestrator.CallContext{User: userAlpha, Message: models.Message{UserID: "user_alpha"}}

	resp, err := mod.Execute(ctx, callCtxAlpha, "create_automation_rule", map[string]interface{}{
		"name":               "Alpha Baby Rule",
		"trigger_type":       "event_mutation",
		"trigger_topic":      "baby",
		"condition_json":     `{"action": "create"}`,
		"action_instruction": "notify: check baby",
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "Alpha Baby Rule")

	// Family Beta attempts to delete Alpha's rule (rule_id: 1)
	userBeta := &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := orchestrator.CallContext{User: userBeta, Message: models.Message{UserID: "user_beta"}}

	_, err = mod.Execute(ctx, callCtxBeta, "delete_automation_rule", map[string]interface{}{
		"rule_id": 1,
	})
	assert.Error(t, err, "Family Beta must not be able to delete Family Alpha's rule")

	// Family Beta lists rules — must not see Alpha's rule
	listResp, err := mod.Execute(ctx, callCtxBeta, "list_automation_rules", map[string]interface{}{})
	require.NoError(t, err)
	assert.NotContains(t, listResp, "Alpha Baby Rule")
}

// TestSEC04_CrossTenant_APIConnectors_Isolation tests API connector isolation.
func TestSEC04_CrossTenant_APIConnectors_Isolation(t *testing.T) {
	repo := newIDORAutomationRepo()
	mod := automation.NewModule(repo, automation.WithAllowLoopback(true))
	ctx := context.Background()

	// Family Alpha registers connector
	userAlpha := &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := orchestrator.CallContext{User: userAlpha, Message: models.Message{UserID: "user_alpha"}}

	_, err := mod.Execute(ctx, callCtxAlpha, "register_external_api", map[string]interface{}{
		"name":         "alpha_secret_api",
		"base_url":     "https://example.com/api",
		"auth_headers": `{"Authorization": "Bearer super-secret-token"}`,
	})
	require.NoError(t, err)

	// Family Beta attempts to list connectors
	userBeta := &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := orchestrator.CallContext{User: userBeta, Message: models.Message{UserID: "user_beta"}}

	listResp, err := mod.Execute(ctx, callCtxBeta, "list_external_apis", map[string]interface{}{})
	require.NoError(t, err)
	assert.NotContains(t, listResp, "alpha_secret_api", "Family Beta must not see Family Alpha's connector")

	// Family Beta attempts to call Alpha's connector by ID
	_, err = mod.Execute(ctx, callCtxBeta, "call_external_api", map[string]interface{}{
		"connector_id": 1,
	})
	assert.Error(t, err, "Family Beta must not be able to invoke Family Alpha's connector by ID")

	// Family Beta attempts to call Alpha's connector by name
	_, err = mod.Execute(ctx, callCtxBeta, "call_external_api", map[string]interface{}{
		"connector_name": "alpha_secret_api",
	})
	assert.Error(t, err, "Family Beta must not be able to invoke Family Alpha's connector by name")
}

// TestSEC04_EmptyFamilyGroup_ConnectorAndRule_Isolation tests isolation between solo users.
func TestSEC04_EmptyFamilyGroup_ConnectorAndRule_Isolation(t *testing.T) {
	repo := newIDORAutomationRepo()
	mod := automation.NewModule(repo)
	ctx := context.Background()

	// Solo 1 creates rule
	userSolo1 := &models.User{ID: "u_solo1", PlatformID: "solo_1", FamilyGroup: ""}
	callCtxSolo1 := orchestrator.CallContext{User: userSolo1, Message: models.Message{UserID: "solo_1"}}

	_, err := mod.Execute(ctx, callCtxSolo1, "create_automation_rule", map[string]interface{}{
		"name":               "Solo1 Rule",
		"trigger_type":       "keyword",
		"trigger_topic":      "tasks",
		"condition_json":     `{"keyword": "urgent"}`,
		"action_instruction": "notify: check task",
	})
	require.NoError(t, err)

	// Solo 2 attempts to delete Solo 1's rule
	userSolo2 := &models.User{ID: "u_solo2", PlatformID: "solo_2", FamilyGroup: ""}
	callCtxSolo2 := orchestrator.CallContext{User: userSolo2, Message: models.Message{UserID: "solo_2"}}

	_, err = mod.Execute(ctx, callCtxSolo2, "delete_automation_rule", map[string]interface{}{
		"rule_id": 1,
	})
	assert.Error(t, err, "Solo 2 must not delete Solo 1's rule even though both have empty family_group")

	// Solo 2 lists rules — must not see Solo 1's rule
	listResp, err := mod.Execute(ctx, callCtxSolo2, "list_automation_rules", map[string]interface{}{})
	require.NoError(t, err)
	assert.NotContains(t, listResp, "Solo1 Rule")
}

// TestSEC04_LegacyEmptyFamilyGroup_IDOR_Vulnerability tests what happens if an existing
// rule or connector in the database has empty FamilyGroup ("") and was created by User 1.
func TestSEC04_LegacyEmptyFamilyGroup_IDOR_Vulnerability(t *testing.T) {
	repo := newIDORAutomationRepo()
	mod := automation.NewModule(repo)
	ctx := context.Background()

	// Seed directly a legacy connector with UserID="user_victim" and empty FamilyGroup=""
	encHeader, _ := crypto.Encrypt([]byte(`{"X-Secret": "victim_token"}`), "")
	_ = repo.CreateAPIConnector(&models.ExternalAPIConnector{
		UserID:               "user_victim",
		FamilyGroup:          "", // legacy unpopulated family_group
		Name:                 "victim_connector",
		BaseURL:              "https://httpbin.org/get",
		DefaultMethod:        "GET",
		EncryptedAuthHeaders: encHeader,
	})

	// Attacker user with UserID="user_attacker", FamilyGroup="attacker_family"
	userAttacker := &models.User{ID: "u_att", PlatformID: "user_attacker", FamilyGroup: "attacker_family"}
	callCtxAttacker := orchestrator.CallContext{User: userAttacker, Message: models.Message{UserID: "user_attacker"}}

	// Attacker calls victim's connector using connector_id: 1
	// Look at line 507 of module.go:
	// if conn == nil || ((conn.FamilyGroup != "" && conn.FamilyGroup != familyGroup) && conn.UserID != userID)
	// If conn.FamilyGroup == "", (conn.FamilyGroup != "" && ...) is false!
	// So conn == nil || false evaluates to false -> PERMISSION GRANTED!
	_, err := mod.Execute(ctx, callCtxAttacker, "call_external_api", map[string]interface{}{
		"connector_id": 1,
	})

	// Check that cross-tenant access to legacy empty FamilyGroup connector is strictly blocked
	assert.Error(t, err, "Attacker must NOT be able to invoke legacy connector with empty FamilyGroup")
	assert.Contains(t, err.Error(), "permission denied")
}

// TestSEC04_CrossTenant_PersonalCRM_Overwrite_Blocked empirically verifies that
// CRM contacts cannot be overwritten across tenants via ID in tool arguments,
// and verifies non-regression on legitimate same-family updates.
func TestSEC04_CrossTenant_PersonalCRM_Overwrite_Blocked(t *testing.T) {
	repo := newIDORAutomationRepo()
	mod := automation.NewModule(repo)
	ctx := context.Background()

	// 1. Family Alpha creates a CRM contact
	userAlpha := &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"}
	callCtxAlpha := orchestrator.CallContext{User: userAlpha, Message: models.Message{UserID: "user_alpha"}}

	respAlpha, err := mod.Execute(ctx, callCtxAlpha, "upsert_personal_crm", map[string]interface{}{
		"person_name":  "Grandma Alice",
		"relationship": "grandmother",
	})
	require.NoError(t, err)
	assert.Contains(t, respAlpha, "Grandma Alice")

	// Verify Contact 1 exists
	contact1, err := repo.GetCRMContact(1)
	require.NoError(t, err)
	require.NotNil(t, contact1)
	assert.Equal(t, "Grandma Alice", contact1.PersonName)
	assert.Equal(t, "family_alpha", contact1.FamilyGroup)

	// 2. Family Beta attempts to overwrite Family Alpha's contact via ID: 1
	userBeta := &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"}
	callCtxBeta := orchestrator.CallContext{User: userBeta, Message: models.Message{UserID: "user_beta"}}

	_, errBeta := mod.Execute(ctx, callCtxBeta, "upsert_personal_crm", map[string]interface{}{
		"id":           1,
		"person_name":  "Hacked Grandma",
		"relationship": "attacker",
	})
	assert.Error(t, errBeta, "Family Beta must NOT be able to overwrite Family Alpha's CRM contact")
	assert.Contains(t, errBeta.Error(), "permission denied")

	// Ensure Contact 1 was NOT altered
	contact1After, err := repo.GetCRMContact(1)
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

	contact1Updated, err := repo.GetCRMContact(1)
	require.NoError(t, err)
	assert.Equal(t, "Grandma Alice Smith", contact1Updated.PersonName)

	// 4. Solo user isolation (empty FamilyGroup)
	userSolo1 := &models.User{ID: "u_solo1", PlatformID: "solo1", FamilyGroup: ""}
	callCtxSolo1 := orchestrator.CallContext{User: userSolo1, Message: models.Message{UserID: "solo1"}}
	_, err = mod.Execute(ctx, callCtxSolo1, "upsert_personal_crm", map[string]interface{}{
		"person_name": "Solo Contact",
	})
	require.NoError(t, err)

	userSolo2 := &models.User{ID: "u_solo2", PlatformID: "solo2", FamilyGroup: ""}
	callCtxSolo2 := orchestrator.CallContext{User: userSolo2, Message: models.Message{UserID: "solo2"}}
	_, errSolo2 := mod.Execute(ctx, callCtxSolo2, "upsert_personal_crm", map[string]interface{}{
		"id":          2,
		"person_name": "Stolen Contact",
	})
	assert.Error(t, errSolo2, "Solo2 must NOT be able to overwrite Solo1's contact")
	assert.Contains(t, errSolo2.Error(), "permission denied")
}

