package durability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	return client
}

// TestAdversarial_SystemEntitiesVeto stress-tests Challenge 1 by attempting to rollback
// every system entity variant (users, pairing_codes, encrypted_credentials, audit_sessions, schema_migrations).
// Module.Rollback MUST unconditionally reject all of them.
func TestAdversarial_SystemEntitiesVeto(t *testing.T) {
	client := newTestDBClient(t)

	bus := events.NewEventBus()
	module := durability.NewModule(client, bus)
	ctx := context.Background()

	scope := householdtest.Scope(t, client, "adversary_family", "adversary_user", "family") // V4-30: the tenant

	systemEntities := []string{
		"User", "users", "user",
		"PairingCode", "pairing_codes", "pairing_code",
		"EncryptedCredential", "encrypted_credentials", "encrypted_credential",
		"AuditSession", "audit_sessions", "audit_session",
		"AuditEvent", "audit_events", "audit_event",
		"SchemaMigration", "schema_migrations", "schema_migration",
	}

	for _, entity := range systemEntities {
		t.Run("Veto_"+entity, func(t *testing.T) {
			mutID := uuid.New().String()
			log := models.MutationLog{
				ID:          mutID,
				Topic:       "system",
				Action:      "delete",
				EntityType:  entity,
				EntityID:    "system-entity-1",
				UserID:      "adversary_user",
				FamilyGroup: "adversary_family",
				BeforeJSON:  `{"id": 1, "data": "tamper"}`,
				AfterJSON:   `{}`,
				IsUndone:    false,
				CreatedAt:   time.Now(),
			}

			err := client.InsertMutationLog(scope, log)
			require.NoError(t, err)
			defer client.DB.Delete(&models.MutationLog{}, "id = ?", mutID)

			res, err := module.Rollback(ctx, scope, mutID)
			assert.Error(t, err, "System entity %q must be rejected unconditionally", entity)
			assert.Nil(t, res)

			// Verify in database that is_undone remains FALSE
			refreshed, err := client.GetMutationLogByID(scope, mutID)
			require.NoError(t, err)
			assert.False(t, refreshed.IsUndone, "Mutation log must NOT be marked undone for rejected entity %s", entity)
		})
	}
}

// TestAdversarial_DomainRollbackLiveDB verifies that rolling back valid domain mutations
// (BabyCareLog, FinancialTransaction, ScheduleEvent) on live PostgreSQL atomically reverts the database row
// and marks mutation_logs.is_undone = true.
func TestAdversarial_DomainRollbackLiveDB(t *testing.T) {
	client := newTestDBClient(t)

	bus := events.NewEventBus()
	module := durability.NewModule(client, bus)
	ctx := context.Background()

	actorID := "durability_test_actor"
	familyGroup := "durability_test_family"
	scope := householdtest.Scope(t, client, familyGroup, actorID, "family") // V4-30: the tenant

	// 1. BabyCareLog: Create action -> Rollback deletes the row
	t.Run("Rollback_BabyCareLog_Create", func(t *testing.T) {
		babyLog := models.BabyCareLog{
			BabyID:         1,
			HouseholdID:    scope.HouseholdID(),
			FamilyGroup:    familyGroup,
			LogType:        "feeding",
			ValueNumeric:   120.0,
			Unit:           "ml",
			Details:        "Formula feed",
			Timestamp:      time.Now(),
			LoggedByUserID: actorID,
		}
		err := client.DB.Create(&babyLog).Error
		require.NoError(t, err)

		mutID := uuid.New().String()
		mLog := models.MutationLog{
			ID:          mutID,
			Topic:       "baby",
			Action:      "create",
			EntityType:  "BabyCareLog",
			EntityID:    fmt.Sprint(babyLog.ID),
			UserID:      actorID,
			FamilyGroup: familyGroup,
			BeforeJSON:  `{}`,
			AfterJSON:   `{}`,
			Summary:     "Logged feeding",
			IsUndone:    false,
			CreatedAt:   time.Now(),
		}
		err = client.InsertMutationLog(scope, mLog)
		require.NoError(t, err)
		defer client.DB.Delete(&models.MutationLog{}, "id = ?", mutID)

		// Execute rollback
		res, err := module.Rollback(ctx, scope, mutID)
		require.NoError(t, err)
		assert.False(t, res.AlreadyUndone)
		assert.Contains(t, res.Detail, fmt.Sprintf("Reverted create '%d' on BabyCareLog", babyLog.ID))

		// Check row is deleted
		var count int64
		client.DB.Model(&models.BabyCareLog{}).Where("id = ?", babyLog.ID).Count(&count)
		assert.Equal(t, int64(0), count, "BabyCareLog row must be deleted after rolling back 'create'")

		// Check mutation log is marked undone
		refreshed, err := client.GetMutationLogByID(scope, mutID)
		require.NoError(t, err)
		assert.True(t, refreshed.IsUndone, "MutationLog.is_undone must be true")
	})

	// 2. FinancialTransaction: Update action -> Rollback restores BeforeJSON state
	t.Run("Rollback_FinancialTransaction_Update", func(t *testing.T) {
		finTx := models.FinancialTransaction{
			UserID:          actorID,
			HouseholdID:     scope.HouseholdID(),
			FamilyGroup:     familyGroup,
			Type:            models.TransactionTypeExpense,
			Category:        models.CategoryGroceries,
			Amount:          150.00,
			Currency:        "USD",
			Merchant:        "Supermarket",
			TransactionDate: time.Now(),
		}
		err := client.DB.Create(&finTx).Error
		require.NoError(t, err)
		defer client.DB.Delete(&models.FinancialTransaction{}, "id = ?", finTx.ID)

		beforeJSONBytes, err := json.Marshal(map[string]interface{}{
			"amount":   100.00,
			"merchant": "FarmerMarket",
		})
		require.NoError(t, err)

		mutID := uuid.New().String()
		mLog := models.MutationLog{
			ID:          mutID,
			Topic:       "finance",
			Action:      "update",
			EntityType:  "FinancialTransaction",
			EntityID:    fmt.Sprint(finTx.ID),
			UserID:      actorID,
			FamilyGroup: familyGroup,
			BeforeJSON:  string(beforeJSONBytes),
			AfterJSON:   `{"amount": 150.00, "merchant": "Supermarket"}`,
			Summary:     "Updated transaction amount",
			IsUndone:    false,
			CreatedAt:   time.Now(),
		}
		err = client.InsertMutationLog(scope, mLog)
		require.NoError(t, err)
		defer client.DB.Delete(&models.MutationLog{}, "id = ?", mutID)

		// Rollback
		res, err := module.Rollback(ctx, scope, mutID)
		require.NoError(t, err)
		assert.False(t, res.AlreadyUndone)

		// Verify database row values reverted to BeforeJSON
		var refreshedTx models.FinancialTransaction
		err = client.DB.Where("id = ?", finTx.ID).First(&refreshedTx).Error
		require.NoError(t, err)
		assert.Equal(t, 100.00, refreshedTx.Amount, "Amount must be reverted to BeforeJSON value 100.00")
		assert.Equal(t, "FarmerMarket", refreshedTx.Merchant, "Merchant must be reverted to FarmerMarket")

		// Verify mutation log is marked undone
		refreshedLog, err := client.GetMutationLogByID(scope, mutID)
		require.NoError(t, err)
		assert.True(t, refreshedLog.IsUndone)
	})

	// 3. ScheduleEvent: Delete action -> Rollback restores row from BeforeJSON
	t.Run("Rollback_ScheduleEvent_Delete", func(t *testing.T) {
		start := time.Now().Add(24 * time.Hour).Truncate(time.Second)
		end := start.Add(1 * time.Hour)

		beforeMap := map[string]interface{}{
			"id":                 999991,
			"user_id":            actorID,
			"family_group":       familyGroup,
			"topic":              "schedule",
			"title":              "Doctor Appointment",
			"start_time":         start.Format(time.RFC3339),
			"end_time":           end.Format(time.RFC3339),
			"sync_to_google":     false,
			"is_shared":          false,
			"created_by_user_id": actorID,
		}
		beforeJSONBytes, err := json.Marshal(beforeMap)
		require.NoError(t, err)

		// Make sure it doesn't exist initially
		client.DB.Where("id = ?", 999991).Delete(&models.ScheduleEvent{})
		defer client.DB.Where("id = ?", 999991).Delete(&models.ScheduleEvent{})

		mutID := uuid.New().String()
		mLog := models.MutationLog{
			ID:          mutID,
			Topic:       "schedule",
			Action:      "delete",
			EntityType:  "ScheduleEvent",
			EntityID:    "999991",
			UserID:      actorID,
			FamilyGroup: familyGroup,
			BeforeJSON:  string(beforeJSONBytes),
			AfterJSON:   `{}`,
			Summary:     "Deleted doctor appointment",
			IsUndone:    false,
			CreatedAt:   time.Now(),
		}
		err = client.InsertMutationLog(scope, mLog)
		require.NoError(t, err)
		defer client.DB.Delete(&models.MutationLog{}, "id = ?", mutID)

		// Execute rollback
		res, err := module.Rollback(ctx, scope, mutID)
		require.NoError(t, err)
		assert.False(t, res.AlreadyUndone)

		// Verify the deleted ScheduleEvent was recreated in the DB
		var restoredEvent models.ScheduleEvent
		err = client.DB.Where("id = ?", 999991).First(&restoredEvent).Error
		require.NoError(t, err, "ScheduleEvent must be recreated from BeforeJSON")
		assert.Equal(t, "Doctor Appointment", restoredEvent.Title)

		// Verify mutation log is marked undone
		refreshedLog, err := client.GetMutationLogByID(scope, mutID)
		require.NoError(t, err)
		assert.True(t, refreshedLog.IsUndone)
	})
}

// TestAdversarial_RollbackAtomicityOnFailure tests that if an SQL statement fails during rollback,
// the entire transaction rolls back and is_undone remains false.
func TestAdversarial_RollbackAtomicityOnFailure(t *testing.T) {
	client := newTestDBClient(t)

	bus := events.NewEventBus()
	module := durability.NewModule(client, bus)
	ctx := context.Background()

	actorID := "durability_atomic_actor"
	familyGroup := "durability_atomic_fam"
	scope := householdtest.Scope(t, client, familyGroup, actorID, "family") // V4-30: the tenant

	mutID := uuid.New().String()
	mLog := models.MutationLog{
		ID:          mutID,
		Topic:       "baby",
		Action:      "update",
		EntityType:  "BabyCareLog",
		EntityID:    "1",
		UserID:      actorID,
		FamilyGroup: familyGroup,
		BeforeJSON:  `{"non_existent_column_fail": "crash"}`,
		AfterJSON:   `{}`,
		IsUndone:    false,
		CreatedAt:   time.Now(),
	}

	err := client.InsertMutationLog(scope, mLog)
	require.NoError(t, err)
	defer client.DB.Delete(&models.MutationLog{}, "id = ?", mutID)

	res, err := module.Rollback(ctx, scope, mutID)
	assert.Error(t, err, "Rollback with invalid columns must return an SQL error")
	assert.Nil(t, res)

	// Verify that is_undone was NOT set to true
	refreshed, err := client.GetMutationLogByID(scope, mutID)
	require.NoError(t, err)
	assert.False(t, refreshed.IsUndone, "Mutation log is_undone must remain false after failed transaction")
}

// TestAdversarial_EngineProcessMessageUndo tests conversational /undo execution through Engine.
func TestAdversarial_EngineProcessMessageUndo(t *testing.T) {
	client := newTestDBClient(t)

	bus := events.NewEventBus()
	durMod := durability.NewModule(client, bus)

	reg := orchestrator.NewRegistry()
	reg.Register(durMod)

	// Clean up past logs for this test user
	testUser := "undo_adversarial_user"
	testFam := "undo_adversarial_fam"
	client.DB.Where("user_id = ?", testUser).Delete(&models.MutationLog{})
	client.DB.Where("platform_id = ?", testUser).Delete(&models.User{})

	scope := householdtest.Scope(t, client, testFam, testUser, "family") // V4-30: the tenant
	user := models.User{
		PlatformID:  testUser,
		HouseholdID: scope.HouseholdID(),
		FamilyGroup: testFam,
		Role:        "family",
	}
	err := client.DB.Create(&user).Error
	require.NoError(t, err)
	defer client.DB.Where("platform_id = ?", testUser).Delete(&models.User{})
	defer client.DB.Where("user_id = ?", testUser).Delete(&models.MutationLog{})

	// Insert a valid mutation
	babyLog := models.BabyCareLog{
		BabyID:         1,
		HouseholdID:    scope.HouseholdID(),
		FamilyGroup:    testFam,
		LogType:        "diaper",
		Details:        "Wet diaper",
		Timestamp:      time.Now(),
		LoggedByUserID: testUser,
	}
	err = client.DB.Create(&babyLog).Error
	require.NoError(t, err)
	defer client.DB.Delete(&models.BabyCareLog{}, "id = ?", babyLog.ID)

	mutID := uuid.New().String()
	mLog := models.MutationLog{
		ID:          mutID,
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		EntityID:    fmt.Sprint(babyLog.ID),
		UserID:      testUser,
		FamilyGroup: testFam,
		BeforeJSON:  `{}`,
		AfterJSON:   `{}`,
		Summary:     "Logged diaper change",
		IsUndone:    false,
		CreatedAt:   time.Now(),
	}
	err = client.InsertMutationLog(scope, mLog)
	require.NoError(t, err)

	// Create a message interceptor to verify response
	var sentTexts []string
	mockTg := &fakeTelegramClient{
		sendMessageFn: func(ctx context.Context, chatID string, text string) error {
			sentTexts = append(sentTexts, text)
			return nil
		},
	}

	// Setup Engine with durability module in registry
	engine := orchestrator.NewEngine(
		nil,                                // consumer
		nil,                                // store
		client,                             // db
		nil,                                // llm
		adapterstest.Registry(mockTg, nil), // channels
		nil,                                // oauth
		nil,                                // gmail
		nil,                                // tasks
		nil,                                // contacts
		nil,                                // tavily
		nil,                                // audio
		nil,                                // publisher
		"UTC",
	)
	engine.SetToolRegistry(reg)

	// Execute /undo
	msg := channel.Inbound{
		Sender:       testUser,
		Conversation: channel.Conversation{ID: "chat-test-123"},
		Channel:      "telegram",
		Text:         "/undo",
	}

	err = engine.ProcessMessage(context.Background(), msg)
	require.NoError(t, err)

	require.NotEmpty(t, sentTexts)
	assert.Contains(t, sentTexts[0], "Successfully Undone", "Confirmation message must report successful rollback")

	// Verify BabyCareLog was deleted
	var count int64
	client.DB.Model(&models.BabyCareLog{}).Where("id = ?", babyLog.ID).Count(&count)
	assert.Equal(t, int64(0), count, "BabyCareLog must be reverted")

	// Verify mutation is marked undone
	refreshed, err := client.GetMutationLogByID(scope, mutID)
	require.NoError(t, err)
	assert.True(t, refreshed.IsUndone)

	// Executing /undo again should inform that no undoable actions remain
	sentTexts = nil
	err = engine.ProcessMessage(context.Background(), msg)
	require.NoError(t, err)
	require.NotEmpty(t, sentTexts)
	assert.Contains(t, sentTexts[0], "No recent undoable actions found")
}

type fakeTelegramClient struct {
	sendMessageFn func(ctx context.Context, chatID string, text string) error
}

func (f *fakeTelegramClient) SendMessage(ctx context.Context, chatID string, text string) error {
	if f.sendMessageFn != nil {
		return f.sendMessageFn(ctx, chatID, text)
	}
	return nil
}

func (f *fakeTelegramClient) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	return nil, nil
}

func (f *fakeTelegramClient) GetFile(ctx context.Context, fileID string) (string, error) {
	return "", nil
}
