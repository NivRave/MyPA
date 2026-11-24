package graph_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/testutil/householdtest"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	client, cleanup := testutil.NewTestDB(t)
	t.Cleanup(cleanup)
	return client
}

// TestSEC06_RollbackMutation_SystemTablesRejected empirically verifies that
// RollbackMutation rejects attempts to mutate system tables (users, pairing_codes, etc.).
func TestSEC06_RollbackMutation_SystemTablesRejected(t *testing.T) {
	client := getTestDBClient(t)

	resolver := &graph.Resolver{DB: client}
	mutResolver := resolver.Mutation()

	systemEntities := []string{
		"users",
		"pairing_codes",
		"encrypted_credentials",
		"audit_sessions",
		"audit_events",
		"schema_migrations",
	}

	// An owner (an admin before V4-35) undoing their own change: the system-table guard decides.
	adminCtx := signedIn(t, client, "", "admin-user", "admin")

	for _, sysEntity := range systemEntities {
		t.Run("RejectSystem_"+sysEntity, func(t *testing.T) {
			mID := uuid.New().String()
			mLog := models.MutationLog{
				ID:          mID,
				Topic:       "system",
				Action:      "delete",
				EntityType:  sysEntity,
				EntityID:    "1",
				UserID:      "admin-user",
				HouseholdID: householdtest.Household(t, client, "admin-user").ID,
				BeforeJSON:  `{"id": "1", "name": "hacked"}`,
				AfterJSON:   `{}`,
				IsUndone:    false,
				CreatedAt:   time.Now(),
			}

			err := client.DB.Create(&mLog).Error
			require.NoError(t, err)
			defer client.DB.Delete(&models.MutationLog{}, "id = ?", mID)

			ok, rErr := mutResolver.RollbackMutation(adminCtx, mID)
			assert.False(t, ok)
			require.Error(t, rErr)
			assert.Contains(t, rErr.Error(), fmt.Sprintf("system entity %q cannot be rolled back", sysEntity))

			// Verify in DB that is_undone remains false
			var refreshed models.MutationLog
			err = client.DB.Where("id = ?", mID).First(&refreshed).Error
			require.NoError(t, err)
			assert.False(t, refreshed.IsUndone, "is_undone must remain false when rollback is rejected")
		})
	}
}

// TestSEC06_RollbackMutation_TransactionRollbackOnStatementFailure tests that
// if an error occurs during rollback execution, the transaction aborts and is_undone remains false.
func TestSEC06_RollbackMutation_TransactionRollbackOnStatementFailure(t *testing.T) {
	client := getTestDBClient(t)

	resolver := &graph.Resolver{DB: client}
	mutResolver := resolver.Mutation()

	adminCtx := signedIn(t, client, "", "admin-user", "admin")

	// A real memory of the household, so the rollback reaches the failing statement (BL-65 reads
	// the row first and refuses a missing one before any statement runs).
	h := householdtest.Household(t, client, "admin-user")
	mem := models.TopicMemory{UserID: "admin-user", HouseholdID: h.ID, FamilyGroup: "admin-user", Topic: "memory",
		Fact: "kept", TagsJSON: "[]", Scope: "family", CreatedByUserID: "admin-user"}
	require.NoError(t, client.DB.Create(&mem).Error)

	mID := uuid.New().String()
	// Create an 'update' mutation targeting TopicMemory where BeforeJSON tries to set a non-existent column,
	// triggering an SQL statement failure during rollback
	mLog := models.MutationLog{
		ID:          mID,
		Topic:       "memory",
		Action:      "update",
		EntityType:  "TopicMemory",
		EntityID:    fmt.Sprint(mem.ID),
		UserID:      "admin-user",
		HouseholdID: householdtest.Household(t, client, "admin-user").ID,
		BeforeJSON:  `{"non_existent_column_fail": "crash"}`,
		AfterJSON:   `{}`,
		IsUndone:    false,
		CreatedAt:   time.Now(),
	}

	err := client.DB.Create(&mLog).Error
	require.NoError(t, err)
	defer client.DB.Delete(&models.MutationLog{}, "id = ?", mID)

	ok, rErr := mutResolver.RollbackMutation(adminCtx, mID)
	assert.False(t, ok)
	require.Error(t, rErr)
	assert.Contains(t, rErr.Error(), "failed to rollback update")
	assert.Contains(t, rErr.Error(), "non_existent_column_fail", "the failure is the statement's, not a missing row")

	// Verify that is_undone remained false in DB
	var refreshed models.MutationLog
	err = client.DB.Where("id = ?", mID).First(&refreshed).Error
	require.NoError(t, err)
	assert.False(t, refreshed.IsUndone, "is_undone MUST remain false after failed rollback transaction")
}

// TestSEC06_RollbackMutation_NonAdminTenantBoundary tests that non-admin users
// cannot rollback mutations belonging to another tenant/family.
func TestSEC06_RollbackMutation_NonAdminTenantBoundary(t *testing.T) {
	client := getTestDBClient(t)

	resolver := &graph.Resolver{DB: client}
	mutResolver := resolver.Mutation()

	// Mutation belonging to family_alpha
	mID := uuid.New().String()
	mLog := models.MutationLog{
		ID:          mID,
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyProfile",
		EntityID:    "123",
		UserID:      "user_alpha",
		HouseholdID: householdtest.Household(t, client, "family_alpha").ID,
		FamilyGroup: "family_alpha",
		BeforeJSON:  `{}`,
		AfterJSON:   `{}`,
		IsUndone:    false,
		CreatedAt:   time.Now(),
	}
	err := client.DB.Create(&mLog).Error
	require.NoError(t, err)
	defer client.DB.Delete(&models.MutationLog{}, "id = ?", mID)

	// User from family_beta attempts rollback
	householdtest.Household(t, client, "family_beta")
	betaCtx := signedIn(t, client, "family_beta", "user_beta", "family")

	ok, rErr := mutResolver.RollbackMutation(betaCtx, mID)
	assert.False(t, ok)
	require.Error(t, rErr)
	assert.Contains(t, rErr.Error(), "permission denied: mutation")

	// Verify in DB that is_undone remains false
	var refreshed models.MutationLog
	err = client.DB.Where("id = ?", mID).First(&refreshed).Error
	require.NoError(t, err)
	assert.False(t, refreshed.IsUndone)
}
