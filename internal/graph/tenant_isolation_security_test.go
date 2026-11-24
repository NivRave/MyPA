package graph_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil/householdtest"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// CHALLENGE 1: ADVERSARIAL ID PARSING & INJECTION
// ============================================================================

func TestAdversarial_MaliciousIDParsing(t *testing.T) {
	client := getTestDBClient(t)
	householdtest.Household(t, client, "family-test")

	bus := events.NewEventBus()
	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	authCtx := signedIn(t, resolver.DB, "family-test", "test-attacker", "admin")

	maliciousIDs := []struct {
		name string
		val  string
	}{
		{"EmptyString", ""},
		{"WhitespaceOnly", "   "},
		{"AlphaString", "abc"},
		{"NegativeOne", "-1"},
		{"NegativeLarge", "-9999999"},
		{"Zero", "0"},
		{"TrimmedZero", "  0  "},
		{"MultiZero", "00000"},
		{"OverflowUint64_21Digits", "999999999999999999999"},
		{"OverflowUint64_MaxPlusOne", "18446744073709551616"},
		{"StringNull", "null"},
		{"StringUndefined", "undefined"},
		{"ScientificNotation", "1e10"},
		{"HexNotation", "0x10"},
		{"SQLInjectionDrop", "; DROP TABLE topic_memories; --"},
		{"SQLInjectionTautology", "1' OR '1'='1"},
		{"XSSPayload", "<script>alert(1)</script>"},
		{"FloatNaN", "NaN"},
		{"FloatInf", "Inf"},
		{"NegativeZero", "-0"},
		{"PositiveZero", "+0"},
		{"NullByte", "\x00"},
	}

	for _, tc := range maliciousIDs {
		t.Run("ID_"+tc.name, func(t *testing.T) {
			// 1. DeleteTopicMemory
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().DeleteTopicMemory(authCtx, tc.val)
				assert.Error(t, err, "DeleteTopicMemory must reject malicious ID %q", tc.val)
				assert.False(t, res)
			})

			// 2. PinTopicMemory
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().PinTopicMemory(authCtx, tc.val, true)
				assert.Error(t, err, "PinTopicMemory must reject malicious ID %q", tc.val)
				assert.Nil(t, res)
			})

			// 3. CompleteSharedTask
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().CompleteSharedTask(authCtx, tc.val, "test-user")
				assert.Error(t, err, "CompleteSharedTask must reject malicious ID %q", tc.val)
				assert.Nil(t, res)
			})

			// 4. DeleteScheduleEvent
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().DeleteScheduleEvent(authCtx, tc.val)
				assert.Error(t, err, "DeleteScheduleEvent must reject malicious ID %q", tc.val)
				assert.False(t, res)
			})

			// 5. LogBabyCare (babyID is first param)
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().LogBabyCare(authCtx, tc.val, "feeding", nil, nil, nil, nil)
				assert.Error(t, err, "LogBabyCare must reject malicious babyID %q", tc.val)
				assert.Nil(t, res)
			})

			// 6. CompleteHomeMaintenance
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().CompleteHomeMaintenance(authCtx, tc.val)
				assert.Error(t, err, "CompleteHomeMaintenance must reject malicious ID %q", tc.val)
				assert.Nil(t, res)
			})

			// 7. ToggleAutomationRule
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().ToggleAutomationRule(authCtx, tc.val, true)
				assert.Error(t, err, "ToggleAutomationRule must reject malicious ID %q", tc.val)
				assert.Nil(t, res)
			})

			// 8. DeleteAutomationRule
			assert.NotPanics(t, func() {
				res, err := resolver.Mutation().DeleteAutomationRule(authCtx, tc.val)
				assert.Error(t, err, "DeleteAutomationRule must reject malicious ID %q", tc.val)
				assert.False(t, res)
			})

			// 9. Query BabyCareLogs (babyID is first param)
			assert.NotPanics(t, func() {
				res, err := resolver.Query().BabyCareLogs(authCtx, tc.val, nil, nil)
				assert.Error(t, err, "BabyCareLogs query must reject malicious babyID %q", tc.val)
				assert.Nil(t, res)
			})
		})
	}
}

// ============================================================================
// CHALLENGE 2: CROSS-TENANT ISOLATION & ADVERSARIAL ACCESS
// ============================================================================

func TestAdversarial_CrossTenantIsolation_Mutations(t *testing.T) {
	client := getTestDBClient(t)
	householdtest.Household(t, client, "FamilyA")
	householdtest.Household(t, client, "VictimFamily")

	bus := events.NewEventBus()
	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	// Non-admin context belonging to FamilyA. Before V4-35 every call named VictimFamily and was
	// refused; since V4-35 no argument can name a household, so each call acts in FamilyA, and
	// VictimFamily must stay empty whatever it does.
	ctxNonAdmin := signedIn(t, resolver.DB, "FamilyA", "attacker-usr", "member")
	victim := householdtest.Household(t, client, "VictimFamily")
	assertVictimUntouched(t, client, victim.ID)

	t.Run("Reject_GeneratePairingCode_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().GeneratePairingCode(ctxNonAdmin, "member")
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveTopicMemory_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveTopicMemory(ctxNonAdmin, "finance", "secret", "balance: 100k", nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_UpsertEntityProfile_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().UpsertEntityProfile(ctxNonAdmin, "people", "contact", "VictimBoss", "{}")
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_UpdateSharedSpace_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().UpdateSharedSpace(ctxNonAdmin, "shared-topic", "attacker-usr", true, true, "always")
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_CreateSharedTask_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().CreateSharedTask(ctxNonAdmin, "chores", "Clean room", nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_CreateScheduleEvent_CrossTenant", func(t *testing.T) {
		now := time.Now()
		later := now.Add(1 * time.Hour)
		_, _ = resolver.Mutation().CreateScheduleEvent(ctxNonAdmin, "work", "Infiltrate", nil, now, later, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveTopicCalendarConfig_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveTopicCalendarConfig(ctxNonAdmin, "work", nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_UpsertBabyProfile_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().UpsertBabyProfile(ctxNonAdmin, "VictimBaby", time.Now().AddDate(-1, 0, 0), "male", nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_LogBabyCare_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().LogBabyCare(ctxNonAdmin, "1", "feeding", nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_UpdateBabyInventory_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().UpdateBabyInventory(ctxNonAdmin, "Diapers", 0, "packs", 2)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveHouseContract_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveHouseContract(ctxNonAdmin, "lease", "Fake Lease", nil, nil, nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveHomeMaintenanceItem_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveHomeMaintenanceItem(ctxNonAdmin, "Fix AC", "cooling", 30, time.Now(), nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveVehicleAsset_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveVehicleAsset(ctxNonAdmin, "VictimCar", "123-ABC", nil, nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_LogFinancialTransaction_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().LogFinancialTransaction(ctxNonAdmin, "expense", "groceries", "Supermarket", 500, "USD", nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SetCategoryBudget_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SetCategoryBudget(ctxNonAdmin, "dining", 500, "USD", nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveRecurringBill_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveRecurringBill(ctxNonAdmin, "Netflix", "entertainment", 15, "USD", "monthly", 1, true)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_RegisterExternalAPI_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().RegisterExternalAPI(ctxNonAdmin, "HackedAPI", "https://evil.com/webhook", nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SaveAutomationRule_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SaveAutomationRule(ctxNonAdmin, "Backdoor", "event_mutation", "baby", "{}", "notify: steal data", nil)
		assertVictimUntouched(t, client, victim.ID)
	})

	t.Run("Reject_SavePersonalCrmContact_CrossTenant", func(t *testing.T) {
		_, _ = resolver.Mutation().SavePersonalCrmContact(ctxNonAdmin, "VictimContact", nil, nil, nil, nil)
		assertVictimUntouched(t, client, victim.ID)
	})
}

func TestAdversarial_CrossTenantIsolation_Queries(t *testing.T) {
	client := getTestDBClient(t)
	householdtest.Household(t, client, "FamilyA")
	householdtest.Household(t, client, "VictimFamily")

	bus := events.NewEventBus()
	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	// The victim household writes marked rows through the API; before V4-35 every query below
	// named VictimFamily and was refused; since V4-35 none can name it, and none may return its data.
	seedVictimData(t, resolver, client)
	ctxNonAdmin := signedIn(t, resolver.DB, "FamilyA", "attacker-usr", "member")

	t.Run("Reject_ActivePairingCodes_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().ActivePairingCodes(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_TopicMemories_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().TopicMemories(ctxNonAdmin, nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_EntityProfiles_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().EntityProfiles(ctxNonAdmin, nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_SharedSpaces_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().SharedSpaces(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_SharedTasks_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().SharedTasks(ctxNonAdmin, nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_ScheduleEvents_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().ScheduleEvents(ctxNonAdmin, nil, nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_BabyProfiles_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().BabyProfiles(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_BabyCareLogs_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().BabyCareLogs(ctxNonAdmin, "1", nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_BabyInventory_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().BabyInventory(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_HouseContracts_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().HouseContracts(ctxNonAdmin, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_HomeMaintenanceItems_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().HomeMaintenanceItems(ctxNonAdmin, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_VehicleAssets_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().VehicleAssets(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_FinancialTransactions_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().FinancialTransactions(ctxNonAdmin, nil, nil)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_CategoryBudgets_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().CategoryBudgets(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_RecurringBills_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().RecurringBills(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_ExternalAPIConnectors_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().ExternalAPIConnectors(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_AutomationRules_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().AutomationRules(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_PersonalCrmContacts_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().PersonalCrmContacts(ctxNonAdmin)
		assertNoVictimData(t, res, err)
	})

	t.Run("Reject_MutationLogs_CrossTenant", func(t *testing.T) {
		res, err := resolver.Query().MutationLogs(ctxNonAdmin, nil, nil)
		assertNoVictimData(t, res, err)
	})
}

// ============================================================================
// CHALLENGE 2B: IDOR ON RECORD MUTATIONS (Cross-Tenant Record Modification)
// ============================================================================

func TestAdversarial_CrossTenant_RecordIDOR(t *testing.T) {
	client := getTestDBClient(t)

	bus := events.NewEventBus()
	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	// Seed records owned by VictimFamily
	victimFG := "VictimFamily_IDOR"
	attackerFG := "AttackerFamily_IDOR"
	victimHousehold := householdtest.Household(t, client, victimFG)
	householdtest.Household(t, client, attackerFG)
	householdtest.Household(t, client, "default")

	adminCtx := signedIn(t, resolver.DB, "default", "admin-seeder", "admin")

	attackerCtx := signedIn(t, resolver.DB, attackerFG, "attacker-usr", "member")

	// Test SaveTopicMemory directly against postgres to verify zero-dimension vector remediation
	t.Run("SaveTopicMemory_PostgresVectorBehavior", func(t *testing.T) {
		res, err := resolver.Mutation().SaveTopicMemory(adminCtx, "secret", "vault", "Victim secret password", nil, nil)
		require.NoError(t, err, "SaveTopicMemory without pre-computed embedding must succeed on PostgreSQL without SQLSTATE 22000")
		require.NotNil(t, res)
		assert.Equal(t, "Victim secret password", res.Fact)
	})

	// Seed TopicMemory directly with valid 3072-dim vector for IDOR tests
	seededMem := models.TopicMemory{
		UserID:          "victim-usr",
		HouseholdID:     victimHousehold.ID,
		FamilyGroup:     victimFG,
		Topic:           "secret",
		Subtopic:        "vault",
		Fact:            "Victim secret password",
		Scope:           "personal",
		TagsJSON:        "[]",
		Embedding:       pgvector.NewVector(make([]float32, 3072)),
		CreatedAt:       time.Now(),
		CreatedByUserID: "victim-usr",
	}
	require.NoError(t, client.DB.Create(&seededMem).Error)
	memIDStr := strconv.FormatUint(uint64(seededMem.ID), 10)

	// Attacker tries to Delete Victim's TopicMemory
	t.Run("IDOR_DeleteTopicMemory_MustFailOrReject", func(t *testing.T) {
		_, err := resolver.Mutation().DeleteTopicMemory(attackerCtx, memIDStr)
		assert.Error(t, err, "Attacker in FamilyA must not be able to delete TopicMemory of VictimFamily")
	})

	// Attacker tries to Pin Victim's TopicMemory
	t.Run("IDOR_PinTopicMemory_MustFailOrReject", func(t *testing.T) {
		_, err := resolver.Mutation().PinTopicMemory(attackerCtx, memIDStr, true)
		assert.Error(t, err, "Attacker in FamilyA must not be able to pin TopicMemory of VictimFamily")
	})

	// 2. Seed an AutomationRule in VictimFamily
	rule, err := resolver.Mutation().SaveAutomationRule(adminCtx, "VictimSecurityRule", "event_mutation", "house", "{}", "notify: alert 911", nil)
	require.NoError(t, err)
	require.NotNil(t, rule)

	// Attacker tries to Toggle Victim's AutomationRule
	t.Run("IDOR_ToggleAutomationRule_VulnerabilityCheck", func(t *testing.T) {
		res, err := resolver.Mutation().ToggleAutomationRule(attackerCtx, rule.ID, false)
		assert.Error(t, err, "Attacker must be rejected when toggling victim's automation rule")
		assert.Contains(t, err.Error(), "permission denied: unauthorized access to automation rule")
		assert.Nil(t, res)
	})

	// Attacker tries to Delete Victim's AutomationRule
	t.Run("IDOR_DeleteAutomationRule_VulnerabilityCheck", func(t *testing.T) {
		res, err := resolver.Mutation().DeleteAutomationRule(attackerCtx, rule.ID)
		assert.Error(t, err, "Attacker must be rejected when deleting victim's automation rule")
		assert.Contains(t, err.Error(), "permission denied: unauthorized access to automation rule")
		assert.False(t, res)
		// Check that rule is still present in database
		var checkRule models.AutomationRule
		dbErr := client.DB.First(&checkRule, rule.ID).Error
		require.NoError(t, dbErr, "Victim's rule must remain untouched in the database")
	})

	// 3. Seed a personal AutomationRule owned by victim (familyGroup == "")
	personalRule := models.AutomationRule{
		UserID:            "victim-usr",
		HouseholdID:       householdtest.Household(t, client, "victim-usr").ID, // an unlabelled owner's own household
		FamilyGroup:       "",
		Name:              "VictimPersonalRule",
		TriggerType:       "event_mutation",
		TriggerTopic:      "memory",
		ConditionJSON:     "{}",
		ActionInstruction: "personal instruction",
		IsActive:          true,
		CreatedAt:         time.Now(),
	}
	require.NoError(t, client.DB.Create(&personalRule).Error)
	personalRuleIDStr := strconv.FormatUint(uint64(personalRule.ID), 10)

	// Attacker tries to Toggle Victim's personal AutomationRule
	t.Run("IDOR_ToggleAutomationRule_PersonalRule_VulnerabilityCheck", func(t *testing.T) {
		res, err := resolver.Mutation().ToggleAutomationRule(attackerCtx, personalRuleIDStr, false)
		assert.Error(t, err, "Attacker must be rejected when toggling victim's personal automation rule")
		assert.Contains(t, err.Error(), "permission denied: unauthorized access to automation rule")
		assert.Nil(t, res)
	})

	// Attacker tries to Delete Victim's personal AutomationRule
	t.Run("IDOR_DeleteAutomationRule_PersonalRule_VulnerabilityCheck", func(t *testing.T) {
		res, err := resolver.Mutation().DeleteAutomationRule(attackerCtx, personalRuleIDStr)
		assert.Error(t, err, "Attacker must be rejected when deleting victim's personal automation rule")
		assert.Contains(t, err.Error(), "permission denied: unauthorized access to automation rule")
		assert.False(t, res)

		var checkRule models.AutomationRule
		dbErr := client.DB.First(&checkRule, personalRule.ID).Error
		require.NoError(t, dbErr, "Victim's personal rule must remain untouched in database")
	})

	// 4. Seed UserPreference for victim
	victimPref := models.UserPreference{
		UserID:               "victim-usr",
		PreferredLanguage:    "fr",
		TonePersona:          "formal",
		DefaultCurrency:      "EUR",
		BriefingTopicsJSON:   "[]",
		StandingInstructions: "Victim private instructions",
		UpdatedAt:            time.Now(),
	}
	require.NoError(t, client.DB.Save(&victimPref).Error)

	// Seed UserPreference for attacker
	attackerPref := models.UserPreference{
		UserID:               "attacker-usr",
		PreferredLanguage:    "en",
		TonePersona:          "casual",
		DefaultCurrency:      "USD",
		BriefingTopicsJSON:   "[]",
		StandingInstructions: "Attacker instructions",
		UpdatedAt:            time.Now(),
	}
	require.NoError(t, client.DB.Save(&attackerPref).Error)

	// Attacker tries to read victim's preference via UserPreference query
	t.Run("IDOR_UserPreference_Query_CannotReadVictim", func(t *testing.T) {
		res, err := resolver.Query().UserPreference(attackerCtx, "victim-usr")
		require.NoError(t, err)
		require.NotNil(t, res, "Resolver should return the caller's own preference without error")
		assert.Equal(t, "attacker-usr", res.UserID, "Caller must only receive their own preference record")
		assert.Equal(t, "en", res.PreferredLanguage, "Attacker must NOT receive victim's French preference")
		assert.Equal(t, "USD", res.DefaultCurrency, "Attacker must NOT receive victim's EUR currency")
		assert.Equal(t, "casual", res.TonePersona, "Attacker must NOT receive victim's formal persona")
	})

	// Attacker without existing preferences queries victim's preference
	t.Run("IDOR_UserPreference_Query_AttackerWithoutPref_ReturnsNil", func(t *testing.T) {
		noPrefCtx := signedIn(t, resolver.DB, attackerFG, "attacker-no-pref", "member")
		res, err := resolver.Query().UserPreference(noPrefCtx, "victim-usr")
		require.NoError(t, err)
		assert.Nil(t, res, "Caller without preference querying victim-usr must receive nil, never victim data")
	})

	// Attacker tries to overwrite victim's preference via SaveUserPreference mutation
	t.Run("IDOR_UserPreference_Save_CannotOverwriteVictim", func(t *testing.T) {
		hackedLang := "ru"
		hackedCurrency := "RUB"
		hackedTone := "hacked"
		res, err := resolver.Mutation().SaveUserPreference(attackerCtx, "victim-usr", &hackedLang, &hackedTone, &hackedCurrency, nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "attacker-usr", res.UserID, "SaveUserPreference must force targetUserID to actorID")

		// Verify victim's preferences in DB were NOT touched
		var checkVictimPref models.UserPreference
		require.NoError(t, client.DB.First(&checkVictimPref, "user_id = ?", "victim-usr").Error)
		assert.Equal(t, "fr", checkVictimPref.PreferredLanguage, "Victim preference must remain unchanged")
		assert.Equal(t, "EUR", checkVictimPref.DefaultCurrency, "Victim currency must remain unchanged")
		assert.Equal(t, "formal", checkVictimPref.TonePersona, "Victim tone must remain unchanged")
	})
}

// ============================================================================
// CHALLENGE 2C: AUDIT SESSIONS & USER SESSIONS TENANT ISOLATION
// ============================================================================

func TestAdversarial_AuditSessions_TenantIsolation(t *testing.T) {
	client := getTestDBClient(t)

	bus := events.NewEventBus()
	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	// An owner sees every member's sessions of their own household (V4-35), the widest view a
	// member has, so the isolation below holds for everyone.
	attackerCtx := signedIn(t, resolver.DB, "AttackerFamily", "attacker-usr", "admin")

	victimFG := "VictimFamily"
	householdtest.Household(t, client, "default")

	// Seed users with real UUIDs to verify uuid::text type casting and tenant scoping
	victimUser := models.User{
		ID:          uuid.New().String(),
		PlatformID:  "victim-plat-" + uuid.New().String()[:8],
		Name:        "Victim User",
		Role:        "family",
		HouseholdID: householdtest.Household(t, client, victimFG).ID,
		FamilyGroup: victimFG,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, client.DB.Create(&victimUser).Error)

	attackerUser := models.User{
		ID:          uuid.New().String(),
		PlatformID:  "attacker-plat-" + uuid.New().String()[:8],
		Name:        "Attacker User",
		Role:        "family",
		HouseholdID: householdtest.Household(t, client, "AttackerFamily").ID,
		FamilyGroup: "AttackerFamily",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, client.DB.Create(&attackerUser).Error)

	// Seed audit sessions where UserID matches UUID
	victimSession := models.AuditSession{
		ID:              uuid.New().String(),
		UserID:          victimUser.ID,
		Source:          "telegram",
		UserPrompt:      "Victim Secret Prompt 12345",
		Status:          "success",
		TotalDurationMs: 150,
		StartTime:       time.Now(),
	}
	require.NoError(t, client.DB.Create(&victimSession).Error)

	attackerSession := models.AuditSession{
		ID:              uuid.New().String(),
		UserID:          attackerUser.ID,
		Source:          "web",
		UserPrompt:      "Attacker Normal Prompt",
		Status:          "success",
		TotalDurationMs: 120,
		StartTime:       time.Now(),
	}
	require.NoError(t, client.DB.Create(&attackerSession).Error)

	// Before V4-35 this named VictimFamily and was refused; no argument can name it now.
	t.Run("AuditSessions_CrossTenantAccessCheck", func(t *testing.T) {
		res, err := resolver.Query().AuditSessions(attackerCtx, nil)
		require.NoError(t, err)
		for _, s := range res {
			assert.NotEqual(t, victimSession.ID, s.ID)
			assert.NotContains(t, s.UserPrompt, "Victim Secret Prompt")
		}
	})

	t.Run("AuditSessions_NilFamilyGroup_ScopesToCallerAndExcludesVictim", func(t *testing.T) {
		res, err := resolver.Query().AuditSessions(attackerCtx, nil)
		require.NoError(t, err, "Postgres must not fail with SQLSTATE 42883 on uuid::text join")
		require.NotEmpty(t, res, "Must return caller's own audit session")
		for _, s := range res {
			assert.NotEqual(t, victimSession.ID, s.ID, "Must NEVER return victim session to attacker")
			assert.NotEqual(t, "Victim Secret Prompt 12345", s.UserPrompt, "Must NEVER leak victim prompt")
		}
	})

	t.Run("UserSessions_CrossTenantAccessCheck", func(t *testing.T) {
		res, err := resolver.Query().UserSessions(attackerCtx)
		require.NoError(t, err)
		for _, s := range res {
			assert.NotEqual(t, victimUser.ID, s.UserID)
		}
	})

	t.Run("UserSessions_NilFamilyGroup_ScopesToCallerAndExcludesVictim", func(t *testing.T) {
		res, err := resolver.Query().UserSessions(attackerCtx)
		require.NoError(t, err, "Postgres must not fail with SQLSTATE 42883 on uuid::text join")
		require.NotEmpty(t, res, "Must return caller's own user session")
		for _, s := range res {
			assert.NotEqual(t, victimUser.ID, s.UserID, "Must NEVER return victim user summary to attacker")
			assert.NotEqual(t, victimUser.PlatformID, s.UserID, "Must NEVER return victim platform ID to attacker")
		}
	})

	// V4-35: the global admin's view of every household's sessions is gone. The operator, signed
	// in, sees no household's sessions but their own (ADR 0002 question 2).
	t.Run("AdminAuditSessions_NoLongerReturnsAllTenants", func(t *testing.T) {
		adminCtx := signedIn(t, resolver.DB, "default", "super-admin", "admin")
		res, err := resolver.Query().AuditSessions(adminCtx, nil)
		require.NoError(t, err)
		for _, s := range res {
			assert.NotEqual(t, victimSession.ID, s.ID, "the victim's session is not the operator's to read")
			assert.NotEqual(t, attackerSession.ID, s.ID, "nor another household's")
		}
	})
}

// ============================================================================
// CHALLENGE 3: MUTATION EVENT EMISSION & SNAPSHOT DURABILITY
// ============================================================================

func TestAdversarial_MutationEventEmission_Snapshots(t *testing.T) {
	client := getTestDBClient(t)

	bus := events.NewEventBus()
	var publishedEvents []events.MutationEvent
	bus.Subscribe(func(ctx context.Context, ev events.MutationEvent) error {
		publishedEvents = append(publishedEvents, ev)
		return nil
	})

	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	fg := "DurabilityTestFamily"
	durabilityHousehold := householdtest.Household(t, client, fg)
	adminCtx := signedIn(t, resolver.DB, fg, "durability-admin", "admin")

	// 1. TopicMemory Lifecycle: Pin (Update) -> Delete
	seededMem := models.TopicMemory{
		UserID:          "durability-admin",
		HouseholdID:     durabilityHousehold.ID,
		FamilyGroup:     fg,
		Topic:           "durability",
		Subtopic:        "test",
		Fact:            "Durability Fact",
		Scope:           "personal",
		TagsJSON:        "[]",
		Embedding:       pgvector.NewVector(make([]float32, 3072)),
		CreatedAt:       time.Now(),
		CreatedByUserID: "durability-admin",
	}
	require.NoError(t, client.DB.Create(&seededMem).Error)
	memIDStr := strconv.FormatUint(uint64(seededMem.ID), 10)

	// Pin (Update)
	publishedEvents = nil
	pinned, err := resolver.Mutation().PinTopicMemory(adminCtx, memIDStr, true)
	require.NoError(t, err)
	require.NotNil(t, pinned)

	require.Len(t, publishedEvents, 1)
	updateEv := publishedEvents[0]
	assert.Equal(t, "update", updateEv.Action)
	assert.NotEmpty(t, updateEv.BeforeJSON, "BeforeJSON for update must be populated")
	assert.NotEmpty(t, updateEv.AfterJSON, "AfterJSON for update must be populated")
	var memBeforeUpdate, memAfterUpdate map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(updateEv.BeforeJSON), &memBeforeUpdate), "BeforeJSON must be valid JSON")
	require.NoError(t, json.Unmarshal([]byte(updateEv.AfterJSON), &memAfterUpdate), "AfterJSON must be valid JSON")

	// Delete
	publishedEvents = nil
	deleted, err := resolver.Mutation().DeleteTopicMemory(adminCtx, memIDStr)
	require.NoError(t, err)
	assert.True(t, deleted)

	require.Len(t, publishedEvents, 1)
	deleteEv := publishedEvents[0]
	assert.Equal(t, "delete", deleteEv.Action)
	assert.NotEmpty(t, deleteEv.BeforeJSON, "BeforeJSON for delete must be populated with deleted entity state")
	assert.Empty(t, deleteEv.AfterJSON, "AfterJSON for delete should be empty string")
	var memBeforeDelete map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(deleteEv.BeforeJSON), &memBeforeDelete), "BeforeJSON must be valid JSON")

	// 2. CompleteSharedTask: Check BeforeJSON and AfterJSON
	publishedEvents = nil
	task, err := resolver.Mutation().CreateSharedTask(adminCtx, "groceries", "Buy Milk", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, task)

	require.Len(t, publishedEvents, 1)
	taskCreateEv := publishedEvents[0]
	assert.Equal(t, "create", taskCreateEv.Action)
	assert.NotEmpty(t, taskCreateEv.AfterJSON)

	// Complete Shared Task
	publishedEvents = nil
	completedTask, err := resolver.Mutation().CompleteSharedTask(adminCtx, task.ID, "durability-admin")
	require.NoError(t, err)
	require.NotNil(t, completedTask)

	require.Len(t, publishedEvents, 1)
	taskCompleteEv := publishedEvents[0]
	assert.Equal(t, "update", taskCompleteEv.Action)
	assert.NotEmpty(t, taskCompleteEv.BeforeJSON, "BeforeJSON must be populated on CompleteSharedTask")
	assert.NotEmpty(t, taskCompleteEv.AfterJSON, "AfterJSON must be populated on CompleteSharedTask")

	// 3. CompleteHomeMaintenance: Check BeforeJSON and AfterJSON
	publishedEvents = nil
	homeItem, err := resolver.Mutation().SaveHomeMaintenanceItem(adminCtx, "Clean Gutters", "exterior", 90, time.Now(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, homeItem)

	publishedEvents = nil
	completedHome, err := resolver.Mutation().CompleteHomeMaintenance(adminCtx, homeItem.ID)
	require.NoError(t, err)
	require.NotNil(t, completedHome)

	require.Len(t, publishedEvents, 1)
	homeEv := publishedEvents[0]
	assert.Equal(t, "update", homeEv.Action)
	assert.NotEmpty(t, homeEv.BeforeJSON, "CompleteHomeMaintenance must populate BeforeJSON")
	assert.NotEmpty(t, homeEv.AfterJSON, "CompleteHomeMaintenance must populate AfterJSON")
}

// victimMarker is in every row the victim household writes in the query test.
const victimMarker = "VICTIM-SECRET-7731"

// assertVictimUntouched fails if any household table holds a row of the victim household.
func assertVictimUntouched(t *testing.T, client *db.Client, victimHouseholdID string) {
	t.Helper()
	var tables []string
	require.NoError(t, client.DB.Raw(`SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'household_id'`).Scan(&tables).Error)
	require.NotEmpty(t, tables)
	for _, table := range tables {
		var n int64
		require.NoError(t, client.DB.Table(table).Where("household_id = ?", victimHouseholdID).Count(&n).Error, table)
		assert.Zero(t, n, "%s has a row in the victim household", table)
	}
}

// assertNoVictimData fails if a query result carries anything the victim household wrote. A
// query may be refused for the caller's role (an adult listing pairing codes), never for anything
// else.
func assertNoVictimData(t *testing.T, res any, err error) {
	t.Helper()
	if err != nil {
		assert.Contains(t, err.Error(), "permission denied", "only a role refusal is expected")
		return
	}
	b, merr := json.Marshal(res)
	require.NoError(t, merr)
	assert.NotContains(t, string(b), victimMarker, "the result carries the victim household's data")
}

// seedVictimData writes one marked row into each table the queries read, as an owner of
// VictimFamily, through the same API.
func seedVictimData(t *testing.T, resolver *graph.Resolver, client *db.Client) {
	t.Helper()
	v := signedIn(t, client, "VictimFamily", "victim-owner", "admin")
	m, q := resolver.Mutation(), resolver.Query()
	family, shared, yes := "family", true, true
	start := time.Now()
	end := start.Add(time.Hour)
	_, err := m.SaveTopicMemory(v, "finance", victimMarker, victimMarker, nil, &family)
	require.NoError(t, err)
	_, err = m.UpsertEntityProfile(v, "people", "contact", victimMarker, `{"x":"`+victimMarker+`"}`)
	require.NoError(t, err)
	_, err = m.UpdateSharedSpace(v, victimMarker, "victim-owner", true, true, "always")
	require.NoError(t, err)
	_, err = m.CreateSharedTask(v, "chores", victimMarker, nil, nil)
	require.NoError(t, err)
	_, err = m.CreateScheduleEvent(v, "work", victimMarker, nil, start, end, nil, nil, &shared)
	require.NoError(t, err)
	baby, err := m.UpsertBabyProfile(v, victimMarker, start.AddDate(-1, 0, 0), "male", nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = m.LogBabyCare(v, baby.ID, "feeding", nil, nil, &[]string{victimMarker}[0], nil)
	require.NoError(t, err)
	_, err = m.UpdateBabyInventory(v, victimMarker, 1, "packs", 2)
	require.NoError(t, err)
	_, err = m.SaveHouseContract(v, "lease", victimMarker, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = m.SaveHomeMaintenanceItem(v, victimMarker, "cooling", 30, end, nil, nil)
	require.NoError(t, err)
	_, err = m.SaveVehicleAsset(v, victimMarker, "123-ABC", nil, nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = m.LogFinancialTransaction(v, "expense", "groceries", victimMarker, 500, "USD", nil, &yes)
	require.NoError(t, err)
	_, err = m.SetCategoryBudget(v, victimMarker, 500, "USD", nil)
	require.NoError(t, err)
	_, err = m.SaveRecurringBill(v, victimMarker, "entertainment", 15, "USD", "monthly", 1, true)
	require.NoError(t, err)
	_, err = m.RegisterExternalAPI(v, victimMarker, "https://93.184.216.34/hook", nil, nil)
	require.NoError(t, err)
	_, err = m.SaveAutomationRule(v, victimMarker, "event_mutation", "baby", "{}", "notify: "+victimMarker, nil)
	require.NoError(t, err)
	_, err = m.SavePersonalCrmContact(v, victimMarker, nil, nil, nil, nil)
	require.NoError(t, err)
	// Sanity: the victim reads back its own data, so an empty attacker result means isolation.
	mems, err := q.TopicMemories(v, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, mems)
}
