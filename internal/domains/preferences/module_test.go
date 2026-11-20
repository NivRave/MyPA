package preferences

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePreferencesRepo is a thread-safe, in-memory db.PreferencesRepository (the preference and
// CRM half of the automation tests' fake, moved with the tools in V4-27).
type fakePreferencesRepo struct {
	mu          sync.RWMutex
	preferences map[string]*models.UserPreference
	contacts    map[uint]*models.PersonalCRMContact
	nextCrmID   uint
}

func newFakePreferencesRepo() *fakePreferencesRepo {
	return &fakePreferencesRepo{
		preferences: make(map[string]*models.UserPreference),
		contacts:    make(map[uint]*models.PersonalCRMContact),
		nextCrmID:   1,
	}
}

func (r *fakePreferencesRepo) UpsertUserPreference(pref *models.UserPreference) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pref.UpdatedAt.IsZero() {
		pref.UpdatedAt = time.Now()
	}
	cp := *pref
	r.preferences[pref.UserID] = &cp
	return nil
}

func (r *fakePreferencesRepo) GetUserPreference(userID string) (*models.UserPreference, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pref, ok := r.preferences[userID]
	if !ok {
		return nil, nil
	}
	cp := *pref
	return &cp, nil
}

func (r *fakePreferencesRepo) UpsertCRMContact(s identity.Scope, contact *models.PersonalCRMContact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	contact.HouseholdID, contact.FamilyGroup = s.HouseholdID(), s.LegacyLabel()
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

func (r *fakePreferencesRepo) GetCRMContact(s identity.Scope, id uint) (*models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.contacts[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakePreferencesRepo) ListCRMContacts(s identity.Scope, query string) ([]models.PersonalCRMContact, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []models.PersonalCRMContact
	q := strings.ToLower(strings.TrimSpace(query))
	for _, c := range r.contacts {
		if c.HouseholdID != s.HouseholdID() {
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

func (r *fakePreferencesRepo) DeleteCRMContact(s identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.contacts, id)
	return nil
}

func TestUserPreferences_UpdateGetAndPromptInjection(t *testing.T) {
	repo := newFakePreferencesRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "u_vip",
			FamilyGroup: "family_alpha",
		},
		Scope:    identitytest.Scope(t, "family_alpha", "u_vip", "family"),
		EventBus: bus,
	}

	// 1. Initial prompt with no preferences has baseline guidance
	promptInitial := mod.SystemPromptSection(context.Background(), callCtx)
	assert.Contains(t, promptInitial, "### User Preferences & Personal CRM:")

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
	repo := newFakePreferencesRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "u_crm_user",
			FamilyGroup: "cohen_home",
		},
		Scope:    identitytest.Scope(t, "cohen_home", "u_crm_user", "family"),
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

func TestPreferences_ToolDeclarations(t *testing.T) {
	mod := NewModule(newFakePreferencesRepo())
	assert.Equal(t, "preferences", mod.Name())

	decls := mod.Declarations()
	assert.Len(t, decls, 4)

	expectedTools := map[string]bool{
		"update_user_preferences": false,
		"get_user_preferences":    false,
		"upsert_personal_crm":     false,
		"query_personal_crm":      false,
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

func TestPreferences_ToolEdgeCases(t *testing.T) {
	repo := newFakePreferencesRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, WithEventBus(bus))

	callCtx := orchestrator.CallContext{
		User:     &models.User{PlatformID: "u_edge"},
		Scope:    identitytest.Scope(t, "u_edge", "u_edge", "family"),
		EventBus: bus,
	}

	ctx := context.Background()

	// Unknown tool
	_, err := mod.Execute(ctx, callCtx, "unknown_tool", nil)
	assert.Error(t, err)

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
