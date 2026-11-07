package automation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAutomationRepo is a thread-safe, in-memory implementation of db.AutomationRepository.
type fakeAutomationRepo struct {
	mu          sync.RWMutex
	connectors  map[uint]*models.ExternalAPIConnector
	rules       map[uint]*models.AutomationRule
	preferences map[string]*models.UserPreference
	contacts    map[uint]*models.PersonalCRMContact
	nextConnID  uint
	nextRuleID  uint
	nextCrmID   uint
}

func newFakeAutomationRepo() *fakeAutomationRepo {
	return &fakeAutomationRepo{
		connectors:  make(map[uint]*models.ExternalAPIConnector),
		rules:       make(map[uint]*models.AutomationRule),
		preferences: make(map[string]*models.UserPreference),
		contacts:    make(map[uint]*models.PersonalCRMContact),
		nextConnID:  1,
		nextRuleID:  1,
		nextCrmID:   1,
	}
}

func (r *fakeAutomationRepo) CreateAPIConnector(c *models.ExternalAPIConnector) error {
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

func (r *fakeAutomationRepo) GetAPIConnector(id uint, familyGroup ...string) (*models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeAutomationRepo) GetAPIConnectorByName(userID, familyGroup, name string) (*models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	normName := strings.ToLower(strings.TrimSpace(name))
	for _, c := range r.connectors {
		if strings.ToLower(c.Name) == normName {
			if userID != "" && familyGroup != "" {
				if c.UserID == userID || c.FamilyGroup == familyGroup {
					cp := *c
					return &cp, nil
				}
			} else if userID != "" {
				if c.UserID == userID {
					cp := *c
					return &cp, nil
				}
			} else {
				cp := *c
				return &cp, nil
			}
		}
	}
	return nil, nil
}

func (r *fakeAutomationRepo) ListAPIConnectors(userID, familyGroup string) ([]models.ExternalAPIConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.ExternalAPIConnector
	for _, c := range r.connectors {
		if userID != "" && familyGroup != "" {
			if c.UserID == userID || c.FamilyGroup == familyGroup {
				list = append(list, *c)
			}
		} else if userID != "" {
			if c.UserID == userID {
				list = append(list, *c)
			}
		} else {
			list = append(list, *c)
		}
	}
	return list, nil
}

func (r *fakeAutomationRepo) DeleteAPIConnector(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.connectors, id)
	return nil
}

func (r *fakeAutomationRepo) CreateAutomationRule(rule *models.AutomationRule) error {
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

func (r *fakeAutomationRepo) GetAutomationRule(id uint) (*models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[id]
	if !ok {
		return nil, nil
	}
	cp := *rule
	return &cp, nil
}

func (r *fakeAutomationRepo) ListAutomationRules(userID, familyGroup, triggerTopic string) ([]models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.AutomationRule
	normTopic := strings.ToLower(strings.TrimSpace(triggerTopic))
	for _, rule := range r.rules {
		if userID != "" && familyGroup != "" {
			if rule.UserID != userID && rule.FamilyGroup != familyGroup {
				continue
			}
		} else if userID != "" && rule.UserID != userID {
			continue
		}

		if normTopic != "" && normTopic != "all" && normTopic != "*" {
			if strings.ToLower(rule.TriggerTopic) != normTopic {
				continue
			}
		}
		list = append(list, *rule)
	}
	return list, nil
}

func (r *fakeAutomationRepo) ListActiveAutomationRules(triggerTopic string) ([]models.AutomationRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.AutomationRule
	normTopic := strings.ToLower(strings.TrimSpace(triggerTopic))
	for _, rule := range r.rules {
		if !rule.IsActive {
			continue
		}
		if normTopic != "" && normTopic != "all" && normTopic != "*" {
			if strings.ToLower(rule.TriggerTopic) != normTopic &&
				rule.TriggerTopic != "*" && rule.TriggerTopic != "" &&
				!strings.EqualFold(rule.TriggerType, "event_mutation") {
				continue
			}
		}
		list = append(list, *rule)
	}
	return list, nil
}

func (r *fakeAutomationRepo) UpdateAutomationRule(rule *models.AutomationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *fakeAutomationRepo) DeleteAutomationRule(id uint, familyGroup ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

func (r *fakeAutomationRepo) UpsertUserPreference(pref *models.UserPreference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pref.UpdatedAt.IsZero() {
		pref.UpdatedAt = time.Now()
	}
	cp := *pref
	r.preferences[pref.UserID] = &cp
	return nil
}

func (r *fakeAutomationRepo) GetUserPreference(userID string) (*models.UserPreference, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pref, ok := r.preferences[userID]
	if !ok {
		return nil, nil
	}
	cp := *pref
	return &cp, nil
}

func (r *fakeAutomationRepo) UpsertCRMContact(contact *models.PersonalCRMContact) error {
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

func (r *fakeAutomationRepo) GetCRMContact(id uint) (*models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.contacts[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeAutomationRepo) ListCRMContacts(userID, familyGroup, query string) ([]models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.PersonalCRMContact
	q := strings.ToLower(strings.TrimSpace(query))
	for _, c := range r.contacts {
		if userID != "" && familyGroup != "" {
			if c.UserID != userID && c.FamilyGroup != familyGroup {
				continue
			}
		} else if userID != "" && c.UserID != userID {
			continue
		}

		if q != "" {
			inName := strings.Contains(strings.ToLower(c.PersonName), q)
			inRel := strings.Contains(strings.ToLower(c.Relationship), q)
			inGifts := strings.Contains(strings.ToLower(c.GiftIdeasAndSizes), q)
			inMed := strings.Contains(strings.ToLower(c.MedicalNotes), q)
			if !inName && !inRel && !inGifts && !inMed {
				continue
			}
		}
		list = append(list, *c)
	}
	return list, nil
}

func (r *fakeAutomationRepo) DeleteCRMContact(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.contacts, id)
	return nil
}

// ==================== UNIT TESTS ====================

func TestExternalAPI_RegistrationAndEncryption(t *testing.T) {
	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "user_123",
			FamilyGroup: "cohen_family",
		},
		EventBus: bus,
	}

	// 1. Register API with auth headers
	authHeaderJSON := `{"Authorization": "Bearer secret_api_token_xyz", "X-Key": "my-secret-key"}`
	args := map[string]interface{}{
		"name":           "weather_service",
		"base_url":       "https://api.weather.example.com",
		"default_method": "GET",
		"auth_headers":   authHeaderJSON,
		"description":    "Real-time weather reports",
	}

	result, err := mod.Execute(context.Background(), callCtx, "register_external_api", args)
	require.NoError(t, err)
	assert.Contains(t, result, "Successfully registered external API connector")

	// 2. Verify stored in repo with encrypted headers
	connectors, err := repo.ListAPIConnectors("user_123", "cohen_family")
	require.NoError(t, err)
	require.Len(t, connectors, 1)

	stored := connectors[0]
	assert.Equal(t, "weather_service", stored.Name)
	assert.Equal(t, "https://api.weather.example.com", stored.BaseURL)
	assert.NotEmpty(t, stored.EncryptedAuthHeaders)
	// Ensure ciphertext is NOT plaintext
	assert.NotContains(t, string(stored.EncryptedAuthHeaders), "secret_api_token_xyz")

	// 3. Decrypt headers and verify integrity
	decrypted, err := DecryptAESGCM(stored.EncryptedAuthHeaders)
	require.NoError(t, err)
	assert.Contains(t, string(decrypted), "secret_api_token_xyz")
	assert.Contains(t, string(decrypted), "my-secret-key")

	// 4. List external APIs tool masks auth headers
	listResult, err := mod.Execute(context.Background(), callCtx, "list_external_apis", nil)
	require.NoError(t, err)
	assert.Contains(t, listResult, "weather_service")
	assert.Contains(t, listResult, `"has_auth": true`)
	assert.NotContains(t, listResult, "secret_api_token_xyz")
}

func TestSSRFProtection_BlocksPrivateAndLocalEndpoints(t *testing.T) {
	repo := newFakeAutomationRepo()
	runner := NewRunner(repo, nil)

	blockedTargets := []string{
		"http://127.0.0.1:8080/admin",
		"http://localhost:3000",
		"http://localhost/metrics",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.1.25:8000/api",
		"http://172.16.0.1/status",
		"http://172.20.0.5:8080",
		"http://192.168.1.1/router",
		"http://192.168.0.100",
		"http://host.docker.internal:9000",
		"http://docker.for.mac.localhost",
		"http://gateway.docker.internal",
		"http://service.local/api",
		"http://database.internal:5432",
		"http://[::1]:8080/test",
		"file:///etc/passwd",
		"ftp://example.com/file",
		"gopher://127.0.0.1:70",
	}

	for _, target := range blockedTargets {
		t.Run("Blocked_"+target, func(t *testing.T) {
			err := runner.ValidateURL(target)
			assert.Error(t, err, "target %s must be blocked by SSRF protection", target)

			// Also verify CallExternal refuses execution
			_, callErr := runner.CallExternal(context.Background(), "GET", target, nil, "")
			assert.Error(t, callErr, "CallExternal to %s must fail with SSRF error", target)
		})
	}
}

func TestExternalAPI_ExecutionWithTestServer(t *testing.T) {
	// Set up local mock server simulating external API endpoint
	receivedAuth := ""
	receivedBody := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success", "temp_c": 24.5}`))
	}))
	defer ts.Close()

	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	// Enable loopback strictly for test mock server
	mod := NewModule(repo, WithEventBus(bus), WithAllowLoopback(true))

	callCtx := orchestrator.CallContext{
		User: &models.User{PlatformID: "user_test", FamilyGroup: "grp_1"},
	}

	// 1. Register test connector with encrypted auth
	authHeaderJSON := `{"Authorization": "Bearer mock-token-12345"}`
	_, err := mod.Execute(context.Background(), callCtx, "register_external_api", map[string]interface{}{
		"name":           "local_mock",
		"base_url":       ts.URL,
		"default_method": "POST",
		"auth_headers":   authHeaderJSON,
	})
	require.NoError(t, err)

	// 2. Call external API via tool
	res, err := mod.Execute(context.Background(), callCtx, "call_external_api", map[string]interface{}{
		"connector_name": "local_mock",
		"path":           "/sensors/temp",
		"method":         "POST",
		"body":           `{"room": "nursery"}`,
	})
	require.NoError(t, err)
	assert.Contains(t, res, "HTTP 200 OK")
	assert.Contains(t, res, `"status": "success"`)
	assert.Contains(t, res, `"temp_c": 24.5`)

	// 3. Verify server received decrypted header and request body
	assert.Equal(t, "Bearer mock-token-12345", receivedAuth)
	assert.Equal(t, `{"room": "nursery"}`, receivedBody)
}

func TestAutomationRule_MatchingAgainstMutationEvents(t *testing.T) {
	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	runner := NewRunner(repo, bus)

	var executedRules []string
	var mu sync.Mutex
	runner.actionExecutor = func(ctx context.Context, rule models.AutomationRule, event events.MutationEvent) error {
		mu.Lock()
		defer mu.Unlock()
		executedRules = append(executedRules, rule.Name)
		return nil
	}

	// Rule 1: Triggers only on topic 'finance' with action 'create'
	err := repo.CreateAutomationRule(&models.AutomationRule{
		UserID:            "user_1",
		Name:              "Finance Create Watcher",
		TriggerType:       "event_mutation",
		TriggerTopic:      "finance",
		ConditionJSON:     `{"action": "create"}`,
		ActionInstruction: "notify: Large expense created",
		IsActive:          true,
	})
	require.NoError(t, err)

	// Rule 2: Triggers on topic 'baby' when summary contains 'fever'
	err = repo.CreateAutomationRule(&models.AutomationRule{
		UserID:            "user_1",
		Name:              "Baby Fever Alert",
		TriggerType:       "keyword",
		TriggerTopic:      "baby",
		ConditionJSON:     `{"keyword": "fever"}`,
		ActionInstruction: "notify: Baby fever reported",
		IsActive:          true,
	})
	require.NoError(t, err)

	// Rule 3: Triggers on any topic when amount in AfterJSON exceeds 500
	err = repo.CreateAutomationRule(&models.AutomationRule{
		UserID:            "user_1",
		Name:              "High Threshold Alert",
		TriggerType:       "threshold",
		TriggerTopic:      "*",
		ConditionJSON:     `{"min_amount": 500}`,
		ActionInstruction: "notify: Mutation over 500",
		IsActive:          true,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Event A: Finance create with amount 750 -> matches Rule 1 & Rule 3
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "finance",
		Action:      "create",
		ActorUserID: "user_1",
		AfterJSON:   `{"amount": 750, "category": "electronics"}`,
		Summary:     "Bought laptop for 750",
	})
	require.NoError(t, err)

	mu.Lock()
	assert.Contains(t, executedRules, "Finance Create Watcher")
	assert.Contains(t, executedRules, "High Threshold Alert")
	assert.NotContains(t, executedRules, "Baby Fever Alert")
	executedRules = nil
	mu.Unlock()

	// Event B: Baby log with normal feeding -> matches no rules
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "baby",
		Action:      "create",
		ActorUserID: "user_1",
		Summary:     "Fed 120ml milk formula",
	})
	require.NoError(t, err)

	mu.Lock()
	assert.Empty(t, executedRules)
	mu.Unlock()

	// Event C: Baby log with fever -> matches Rule 2
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "baby",
		Action:      "update",
		ActorUserID: "user_1",
		Summary:     "Baby has a high fever of 39.1C",
	})
	require.NoError(t, err)

	mu.Lock()
	assert.Contains(t, executedRules, "Baby Fever Alert")
	assert.NotContains(t, executedRules, "Finance Create Watcher")
	assert.NotContains(t, executedRules, "High Threshold Alert")
	mu.Unlock()
}

func TestUserPreferences_UpdateGetAndPromptInjection(t *testing.T) {
	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "u_vip",
			FamilyGroup: "family_alpha",
		},
		EventBus: bus,
	}

	// 1. Initial prompt with no preferences has baseline guidance
	promptInitial := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, promptInitial, "### Runtime Customization, Preferences & Personal CRM:")

	// 2. Update user preferences
	_, err := mod.Execute(context.Background(), callCtx, "update_user_preferences", map[string]interface{}{
		"preferred_language":    "Hebrew",
		"tone_persona":          "Analytical & Structured",
		"default_currency":      "EUR",
		"quiet_hours_start":     "23:00",
		"quiet_hours_end":       "07:00",
		"briefing_topics":       `["schedule", "finance", "baby"]`,
		"standing_instructions": "Always include nutritional breakdown when discussing meals.",
	})
	require.NoError(t, err)

	// 3. Get user preferences
	getRes, err := mod.Execute(context.Background(), callCtx, "get_user_preferences", nil)
	require.NoError(t, err)
	assert.Contains(t, getRes, "Analytical & Structured")
	assert.Contains(t, getRes, "EUR")
	assert.Contains(t, getRes, "Hebrew")

	// 4. SystemPromptSection dynamically injects updated preferences
	promptUpdated := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, promptUpdated, "Tone/Persona: Analytical & Structured")
	assert.Contains(t, promptUpdated, "Default Currency: EUR")
	assert.Contains(t, promptUpdated, "Preferred Language: Hebrew")
	assert.Contains(t, promptUpdated, "Quiet Hours: 23:00 to 07:00")
	assert.Contains(t, promptUpdated, "Always include nutritional breakdown when discussing meals.")
}

func TestPersonalCRM_UpsertQueryAndUpcomingAlerts(t *testing.T) {
	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "u_crm_user",
			FamilyGroup: "cohen_home",
		},
		EventBus: bus,
	}

	// Compute a dynamic birthday 5 days from today
	now := time.Now()
	bdayIn5Days := now.Add(5 * 24 * time.Hour)
	bdayStr := bdayIn5Days.Format("2006-01-02")

	// 1. Upsert Contact A (Birthday in 5 days)
	datesA := fmt.Sprintf(`[{"label": "Birthday", "date": "%s"}]`, bdayStr)
	_, err := mod.Execute(context.Background(), callCtx, "upsert_personal_crm", map[string]interface{}{
		"person_name":          "Dana Cohen",
		"relationship":         "Sister",
		"important_dates":      datesA,
		"gift_ideas_and_sizes": "Loves vintage books, sweater size S, shoe size 38",
		"medical_notes":        "Mild nut allergy",
	})
	require.NoError(t, err)

	// 2. Upsert Contact B (Anniversary 6 months from now - outside 30 days window)
	futureDate := now.Add(180 * 24 * time.Hour)
	datesB := fmt.Sprintf(`[{"label": "Wedding Anniversary", "date": "%s"}]`, futureDate.Format("2006-01-02"))
	_, err = mod.Execute(context.Background(), callCtx, "upsert_personal_crm", map[string]interface{}{
		"person_name":          "Uncle David",
		"relationship":         "Uncle",
		"important_dates":      datesB,
		"gift_ideas_and_sizes": "Red wine, chess set",
	})
	require.NoError(t, err)

	// 3. Query Personal CRM
	queryRes, err := mod.Execute(context.Background(), callCtx, "query_personal_crm", map[string]interface{}{
		"query": "vintage books",
	})
	require.NoError(t, err)
	assert.Contains(t, queryRes, "Dana Cohen")
	assert.NotContains(t, queryRes, "Uncle David")

	// Query by relationship
	queryRel, err := mod.Execute(context.Background(), callCtx, "query_personal_crm", map[string]interface{}{
		"relationship": "Uncle",
	})
	require.NoError(t, err)
	assert.Contains(t, queryRel, "Uncle David")

	// 4. SystemPromptSection checks:
	// Dana's birthday in 5 days MUST appear in alerts
	// Uncle David's 6-month date MUST NOT appear in the next 30 days alerts
	prompt := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, prompt, "Dana Cohen")
	assert.Contains(t, prompt, "Sister")
	assert.Contains(t, prompt, "Birthday")
	assert.Contains(t, prompt, "in 5 days")
	assert.Contains(t, prompt, "vintage books")
	assert.NotContains(t, prompt, "Uncle David")
}

func TestAutomation_ToolDeclarations(t *testing.T) {
	mod := NewModule(newFakeAutomationRepo())
	assert.Equal(t, "automation", mod.Name())

	decls := mod.Declarations()
	assert.Len(t, decls, 13)

	expectedTools := map[string]bool{
		"register_external_api":   false,
		"list_external_apis":      false,
		"call_external_api":       false,
		"create_automation_rule":  false,
		"list_automation_rules":   false,
		"delete_automation_rule":  false,
		"update_user_preferences": false,
		"get_user_preferences":    false,
		"upsert_personal_crm":     false,
		"query_personal_crm":      false,
		"schedule_workflow":       false,
		"list_workflows":          false,
		"delete_workflow":         false,
	}

	for _, d := range decls {
		require.NotNil(t, d)
		assert.NotEmpty(t, d.Name)
		assert.NotEmpty(t, d.Description)
		assert.NotNil(t, d.Parameters)
		expectedTools[d.Name] = true
	}

	for tool, found := range expectedTools {
		assert.True(t, found, "tool %s must be declared", tool)
	}
}

func TestEvaluateCondition_OperatorsAndThresholds(t *testing.T) {
	ev := events.MutationEvent{
		Topic:       "finance",
		Action:      "create",
		EntityType:  "Expense",
		Summary:     "Bought flight tickets to Paris",
		AfterJSON:   `{"amount": 1200, "merchant": "AirFrance", "category": "travel"}`,
		ActorUserID: "user_alice",
		FamilyGroup: "family_paris",
	}

	// 1. Empty condition matches
	m, err := EvaluateCondition("", ev)
	require.NoError(t, err)
	assert.True(t, m)

	m, err = EvaluateCondition("{}", ev)
	require.NoError(t, err)
	assert.True(t, m)

	// 2. Action match
	m, err = EvaluateCondition(`{"action": "create"}`, ev)
	require.NoError(t, err)
	assert.True(t, m)

	m, err = EvaluateCondition(`{"action": "delete"}`, ev)
	require.NoError(t, err)
	assert.False(t, m)

	// 3. Keyword in summary
	m, err = EvaluateCondition(`{"keyword": "paris"}`, ev)
	require.NoError(t, err)
	assert.True(t, m)

	m, err = EvaluateCondition(`{"keyword": "tokyo"}`, ev)
	require.NoError(t, err)
	assert.False(t, m)

	// 4. Threshold on amount
	m, err = EvaluateCondition(`{"threshold": 1000}`, ev)
	require.NoError(t, err)
	assert.True(t, m)

	m, err = EvaluateCondition(`{"min_amount": 1500}`, ev)
	require.NoError(t, err)
	assert.False(t, m)

	// 5. Operator comparisons
	m, err = EvaluateCondition(`{"field": "amount", "operator": ">", "value": 1000}`, ev)
	require.NoError(t, err)
	assert.True(t, m)

	m, err = EvaluateCondition(`{"field": "amount", "operator": "<=", "value": 1000}`, ev)
	require.NoError(t, err)
	assert.False(t, m)

	m, err = EvaluateCondition(`{"field": "merchant", "operator": "contains", "value": "france"}`, ev)
	require.NoError(t, err)
	assert.True(t, m)
}

func TestAutomation_ToolEdgeCases(t *testing.T) {
	repo := newFakeAutomationRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User:     &models.User{PlatformID: "u_edge"},
		EventBus: bus,
	}

	ctx := context.Background()

	// Unknown tool
	_, err := mod.Execute(ctx, callCtx, "unknown_tool", nil)
	assert.Error(t, err)

	// Missing name in register
	_, err = mod.Execute(ctx, callCtx, "register_external_api", map[string]interface{}{"base_url": "https://example.com"})
	assert.Error(t, err)

	// Missing base_url in register
	_, err = mod.Execute(ctx, callCtx, "register_external_api", map[string]interface{}{"name": "foo"})
	assert.Error(t, err)

	// Call non-existent connector
	_, err = mod.Execute(ctx, callCtx, "call_external_api", map[string]interface{}{"connector_name": "ghost_connector"})
	assert.Error(t, err)

	// Delete non-existent rule
	_, err = mod.Execute(ctx, callCtx, "delete_automation_rule", map[string]interface{}{"rule_id": 9999})
	require.NoError(t, err) // Idempotent delete

	// Missing person_name in CRM
	_, err = mod.Execute(ctx, callCtx, "upsert_personal_crm", map[string]interface{}{})
	assert.Error(t, err)

	// Update existing CRM contact
	upsertRes, err := mod.Execute(ctx, callCtx, "upsert_personal_crm", map[string]interface{}{
		"person_name":  "Tom Hardy",
		"relationship": "Friend",
	})
	require.NoError(t, err)
	assert.Contains(t, upsertRes, "Successfully saved CRM contact")

	// Update Tom Hardy with ID 1
	upsertRes2, err := mod.Execute(ctx, callCtx, "upsert_personal_crm", map[string]interface{}{
		"id":           1,
		"person_name":  "Tom Hardy",
		"relationship": "Best Friend",
	})
	require.NoError(t, err)
	assert.Contains(t, upsertRes2, "Best Friend")
}

func TestCalculateDaysUntil(t *testing.T) {
	refTime := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	// Today
	days, ok := calculateDaysUntil("2026-11-03", refTime)
	assert.True(t, ok)
	assert.Equal(t, 0, days)

	// Tomorrow
	days, ok = calculateDaysUntil("2026-11-04", refTime)
	assert.True(t, ok)
	assert.Equal(t, 1, days)

	// In 10 days (Nov 13)
	days, ok = calculateDaysUntil("11-13", refTime)
	assert.True(t, ok)
	assert.Equal(t, 10, days)

	// Past date this year (Oct 10) -> next year occurrence
	days, ok = calculateDaysUntil("10-10", refTime)
	assert.True(t, ok)
	assert.Greater(t, days, 300)

	// Invalid date
	_, ok = calculateDaysUntil("not-a-date", refTime)
	assert.False(t, ok)
}
