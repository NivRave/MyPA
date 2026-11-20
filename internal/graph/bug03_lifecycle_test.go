package graph

import (
	"testing"

	"github.com/nivik/mypa/internal/identity"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/netguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifecycleAutoRepo simulates in-memory persistence for BUG-03 lifecycle testing
type lifecycleAutoRepo struct {
	mockAutoRepo
	apis  []models.ExternalAPIConnector
	rules []models.AutomationRule
}

func (r *lifecycleAutoRepo) CreateAPIConnector(s identity.Scope, connector *models.ExternalAPIConnector) error {
	stampScope(s, &connector.HouseholdID, &connector.FamilyGroup)
	connector.ID = uint(len(r.apis) + 1)
	r.apis = append(r.apis, *connector)
	return nil
}

func (r *lifecycleAutoRepo) ListAPIConnectors(s identity.Scope) ([]models.ExternalAPIConnector, error) {
	var result []models.ExternalAPIConnector
	for _, api := range r.apis {
		if api.HouseholdID == s.HouseholdID() {
			result = append(result, api)
		}
	}
	return result, nil
}

func (r *lifecycleAutoRepo) CreateAutomationRule(s identity.Scope, rule *models.AutomationRule) error {
	stampScope(s, &rule.HouseholdID, &rule.FamilyGroup)
	rule.ID = uint(len(r.rules) + 1)
	r.rules = append(r.rules, *rule)
	return nil
}

func (r *lifecycleAutoRepo) ListAutomationRules(s identity.Scope, triggerTopic string) ([]models.AutomationRule, error) {
	var result []models.AutomationRule
	for _, rule := range r.rules {
		if rule.HouseholdID == s.HouseholdID() {
			if triggerTopic == "" || rule.TriggerTopic == triggerTopic {
				result = append(result, rule)
			}
		}
	}
	return result, nil
}

// TestBUG03_Lifecycle_RetentionAndImmediateVisibility verifies that newly created
// external APIs and automation rules retain their familyGroup and appear immediately in tenant queries
func TestBUG03_Lifecycle_RetentionAndImmediateVisibility(t *testing.T) {
	netguard.SetAllowLoopbackForTesting(true)
	defer netguard.SetAllowLoopbackForTesting(false)

	bus := events.NewEventBus()
	autoRepo := &lifecycleAutoRepo{}

	resolver := NewResolver(ResolverConfig{
		EventBus:       bus,
		AutomationRepo: autoRepo,
		Households:     labelStore("Smith Family", "Jones Family", "admin-system"),
	})

	tenantFamily := "Smith Family"
	otherFamily := "Jones Family"

	ctxTenantUser := signedIn(t, resolver, tenantFamily, "user-smith-1", "member")

	// --- 1. Test External API Registration & Query Visibility ---
	apiName := "SmartHomeWebhook"
	apiURL := "http://127.0.0.1:9090/webhook"
	apiMethod := "POST"
	apiDesc := "Controls living room lighting"

	// Register API with explicit familyGroup
	createdAPI, err := resolver.Mutation().RegisterExternalAPI(
		ctxTenantUser,
		apiName,
		apiURL,
		&apiMethod,
		&apiDesc,
	)
	require.NoError(t, err)
	require.NotNil(t, createdAPI)
	assert.Equal(t, tenantFamily, createdAPI.FamilyGroup, "Created API must retain familyGroup")
	assert.Equal(t, apiName, createdAPI.Name)

	// Query immediately as tenant user for tenantFamily
	tenantAPIs, err := resolver.Query().ExternalAPIConnectors(ctxTenantUser)
	require.NoError(t, err)
	require.Len(t, tenantAPIs, 1, "Registered API must appear immediately in tenant queries")
	assert.Equal(t, apiName, tenantAPIs[0].Name)
	assert.Equal(t, tenantFamily, tenantAPIs[0].FamilyGroup)

	// --- 2. Test Automation Rule Creation & Query Visibility ---
	ruleName := "BabyCareTelegramAlert"
	triggerType := "mutation"
	triggerTopic := "baby"
	condition := `{"logType":"feeding"}`
	action := "Send notification to partner"

	createdRule, err := resolver.Mutation().SaveAutomationRule(
		ctxTenantUser,
		ruleName,
		triggerType,
		triggerTopic,
		condition,
		action,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, createdRule)
	assert.Equal(t, tenantFamily, createdRule.FamilyGroup, "Created rule must retain familyGroup")
	assert.Equal(t, ruleName, createdRule.Name)

	// Query immediately as tenant user for tenantFamily
	tenantRules, err := resolver.Query().AutomationRules(ctxTenantUser)
	require.NoError(t, err)
	require.Len(t, tenantRules, 1, "Registered rule must appear immediately in tenant queries")
	assert.Equal(t, ruleName, tenantRules[0].Name)
	assert.Equal(t, tenantFamily, tenantRules[0].FamilyGroup)

	// --- 3. Verify Isolation: Other Tenant Querying Never Sees Smith Family Data ---
	ctxOtherUser := signedIn(t, resolver, otherFamily, "user-jones-1", "member")

	otherAPIs, err := resolver.Query().ExternalAPIConnectors(ctxOtherUser)
	require.NoError(t, err)
	assert.Empty(t, otherAPIs, "Other tenant must not see Smith Family APIs")

	otherRules, err := resolver.Query().AutomationRules(ctxOtherUser)
	require.NoError(t, err)
	assert.Empty(t, otherRules, "Other tenant must not see Smith Family automation rules")

	// --- 4. V4-35: no caller can name Smith Family; an owner of another household (an admin
	// before V4-35, who could query any family) sees none of its data either ---
	ctxAdmin := signedIn(t, resolver, "admin-system", "admin-sys", "admin")

	adminAPIs, err := resolver.Query().ExternalAPIConnectors(ctxAdmin)
	require.NoError(t, err)
	assert.Empty(t, adminAPIs, "an owner of another household must not see Smith Family APIs")

	adminRules, err := resolver.Query().AutomationRules(ctxAdmin)
	require.NoError(t, err)
	assert.Empty(t, adminRules)
}
