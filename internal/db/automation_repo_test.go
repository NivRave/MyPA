package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutomationRepo_APIConnectors_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewAutomationRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Create connector for Tenant A
	connA := models.ExternalAPIConnector{
		UserID:               userA,
		FamilyGroup:          famA,
		Name:                 "HomeAssistant",
		BaseURL:              "https://ha.internal.local",
		DefaultMethod:        "POST",
		EncryptedAuthHeaders: []byte("encrypted-ha-token"),
		Description:          "Smart home automation bridge",
	}
	require.NoError(t, repo.CreateAPIConnector(&connA))
	require.NotZero(t, connA.ID)

	// 2. Create connector for Tenant B
	connB := models.ExternalAPIConnector{
		UserID:               userB,
		FamilyGroup:          famB,
		Name:                 "PhilipsHue",
		BaseURL:              "https://hue.internal.local",
		DefaultMethod:        "GET",
		EncryptedAuthHeaders: []byte("encrypted-hue-token"),
	}
	require.NoError(t, repo.CreateAPIConnector(&connB))
	require.NotZero(t, connB.ID)

	// 3. Positive lookup and tenant isolation
	gotA, err := repo.GetAPIConnector(connA.ID, famA)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "HomeAssistant", gotA.Name)
	assert.Equal(t, famA, gotA.FamilyGroup)

	// Tenant B querying Tenant A's connector returns nil
	gotLeak, err := repo.GetAPIConnector(connA.ID, famB)
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Tenant B must not access Tenant A's connector")

	// GetAPIConnectorByName
	byNameA, err := repo.GetAPIConnectorByName(userA, famA, "homeassistant")
	require.NoError(t, err)
	require.NotNil(t, byNameA)
	assert.Equal(t, connA.ID, byNameA.ID)

	byNameLeak, err := repo.GetAPIConnectorByName(userA, famA, "philipshue")
	require.NoError(t, err)
	assert.Nil(t, byNameLeak, "Tenant A must not find Tenant B's connector by name")

	// 4. ListAPIConnectors
	listA, err := repo.ListAPIConnectors(userA, famA)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, connA.ID, listA[0].ID)

	listB, err := repo.ListAPIConnectors(userB, famB)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, connB.ID, listB[0].ID)

	// 5. DeleteAPIConnector
	require.NoError(t, repo.DeleteAPIConnector(connA.ID))
	deletedA, err := repo.GetAPIConnector(connA.ID, famA)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}

func TestAutomationRepo_AutomationRules_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewAutomationRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Create rule for Tenant A
	ruleA := models.AutomationRule{
		UserID:            userA,
		FamilyGroup:       famA,
		Name:              "Diaper Restock Alert",
		TriggerType:       "event_mutation",
		TriggerTopic:      "baby",
		ConditionJSON:     `{"log_type":"diaper"}`,
		ActionInstruction: "Check diaper inventory",
		IsActive:          true,
	}
	require.NoError(t, repo.CreateAutomationRule(&ruleA))
	require.NotZero(t, ruleA.ID)

	// 2. Create rule for Tenant B
	ruleB := models.AutomationRule{
		UserID:            userB,
		FamilyGroup:       famB,
		Name:              "High Expense Flag",
		TriggerType:       "threshold",
		TriggerTopic:      "finance",
		ConditionJSON:     `{"amount_gt":500}`,
		ActionInstruction: "Notify family admin",
		IsActive:          true,
	}
	require.NoError(t, repo.CreateAutomationRule(&ruleB))
	require.NotZero(t, ruleB.ID)

	// 3. Positive lookup and tenant isolation
	gotA, err := repo.GetAutomationRule(ruleA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Diaper Restock Alert", gotA.Name)

	listA, err := repo.ListAutomationRules(userA, famA, "all")
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, ruleA.ID, listA[0].ID)

	listB, err := repo.ListAutomationRules(userB, famB, "all")
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, ruleB.ID, listB[0].ID)

	// 4. ListActiveAutomationRules by topic
	activeBabyRules, err := repo.ListActiveAutomationRules("baby")
	require.NoError(t, err)
	require.Len(t, activeBabyRules, 1)
	assert.Equal(t, ruleA.ID, activeBabyRules[0].ID)

	// Update to inactive
	ruleA.IsActive = false
	require.NoError(t, repo.UpdateAutomationRule(&ruleA))

	activeBabyAfter, err := repo.ListActiveAutomationRules("baby")
	require.NoError(t, err)
	assert.Empty(t, activeBabyAfter, "Deactivated rule must not be returned by ListActiveAutomationRules")

	// 5. DeleteAutomationRule with tenant scoping
	// Attempt delete with wrong tenant should not delete
	err = repo.DeleteAutomationRule(ruleA.ID, famB)
	require.NoError(t, err)

	recheckStillThere, err := repo.GetAutomationRule(ruleA.ID)
	require.NoError(t, err)
	require.NotNil(t, recheckStillThere, "Rule should not be deleted by unauthorized tenant")

	// Delete with correct tenant
	err = repo.DeleteAutomationRule(ruleA.ID, famA)
	require.NoError(t, err)

	recheckDeleted, err := repo.GetAutomationRule(ruleA.ID)
	require.NoError(t, err)
	assert.Nil(t, recheckDeleted)
}

func TestAutomationRepo_UserPreferences_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewAutomationRepository(client.DB)

	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Upsert preference for User A
	prefA := models.UserPreference{
		UserID:               userA,
		PreferredLanguage:    "en",
		TonePersona:          "concise",
		DefaultCurrency:      "USD",
		QuietHoursStart:      "22:00",
		QuietHoursEnd:        "07:00",
		BriefingTopicsJSON:   `["schedule","weather","baby"]`,
		StandingInstructions: "Always provide bullet points.",
	}
	require.NoError(t, repo.UpsertUserPreference(&prefA))

	// 2. Upsert preference for User B
	prefB := models.UserPreference{
		UserID:               userB,
		PreferredLanguage:    "es",
		TonePersona:          "warm",
		DefaultCurrency:      "EUR",
		StandingInstructions: "Responde siempre en español.",
	}
	require.NoError(t, repo.UpsertUserPreference(&prefB))

	// 3. Positive lookup and user isolation
	gotA, err := repo.GetUserPreference(userA)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "en", gotA.PreferredLanguage)
	assert.Equal(t, "Always provide bullet points.", gotA.StandingInstructions)

	gotB, err := repo.GetUserPreference(userB)
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.Equal(t, "es", gotB.PreferredLanguage)
	assert.Equal(t, "Responde siempre en español.", gotB.StandingInstructions)

	// Non-existent user returns nil
	gotNonExistent, err := repo.GetUserPreference("usr-nonexistent")
	require.NoError(t, err)
	assert.Nil(t, gotNonExistent)

	// 4. Update preference in-place
	prefA.PreferredLanguage = "fr"
	prefA.TonePersona = "formal"
	require.NoError(t, repo.UpsertUserPreference(&prefA))

	recheckA, err := repo.GetUserPreference(userA)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, "fr", recheckA.PreferredLanguage)
	assert.Equal(t, "formal", recheckA.TonePersona)
}

func TestAutomationRepo_PersonalCRM_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewAutomationRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Create contacts for Family A
	contactA := models.PersonalCRMContact{
		UserID:             userA,
		FamilyGroup:        famA,
		PersonName:         "Grandma Rose",
		Relationship:       "grandmother",
		ImportantDatesJSON: `{"birthday":"1950-06-12"}`,
		GiftIdeasAndSizes:  "Knitting yarn, Shoe size 7",
		MedicalNotes:       "Diabetic, low sugar gifts only",
		UpdatedAt:          time.Now(),
	}
	require.NoError(t, repo.UpsertCRMContact(&contactA))
	require.NotZero(t, contactA.ID)

	// 2. Create contact for Family B
	contactB := models.PersonalCRMContact{
		UserID:             userB,
		FamilyGroup:        famB,
		PersonName:         "Uncle Bob",
		Relationship:       "uncle",
		GiftIdeasAndSizes:  "Books on history",
		UpdatedAt:          time.Now(),
	}
	require.NoError(t, repo.UpsertCRMContact(&contactB))
	require.NotZero(t, contactB.ID)

	// 3. Positive lookup and cross-tenant isolation
	gotA, err := repo.GetCRMContact(contactA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Grandma Rose", gotA.PersonName)

	listA, err := repo.ListCRMContacts(userA, famA, "Rose")
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, contactA.ID, listA[0].ID)

	// Cross-tenant search
	listLeak, err := repo.ListCRMContacts(userA, famA, "Bob")
	require.NoError(t, err)
	assert.Empty(t, listLeak, "Family A must not see Family B's contact")

	listB, err := repo.ListCRMContacts(userB, famB, "Bob")
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, contactB.ID, listB[0].ID)

	// 4. Update contact
	contactA.GiftIdeasAndSizes = "Wool socks, Gardening tools"
	require.NoError(t, repo.UpsertCRMContact(&contactA))

	recheckA, err := repo.GetCRMContact(contactA.ID)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, "Wool socks, Gardening tools", recheckA.GiftIdeasAndSizes)

	// 5. Delete contact
	require.NoError(t, repo.DeleteCRMContact(contactA.ID))
	deletedA, err := repo.GetCRMContact(contactA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}
