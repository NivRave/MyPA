package graph_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil"
	"github.com/nivik/mypa/internal/graph"
	"github.com/nivik/mypa/internal/models"
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

	ctx := context.Background()
	// Admin auth claims
	adminClaims := &graph.AuthClaims{
		UserID: "admin-user",
		Role:   "admin",
	}
	adminCtx := graph.WithClaims(ctx, adminClaims)

	for _, sysEntity := range systemEntities {
		t.Run("RejectSystem_"+sysEntity, func(t *testing.T) {
			mID := uuid.New().String()
			mLog := models.MutationLog{
				ID:          mID,
				Topic:       "system",
				Action:      "delete",
				EntityType:  sysEntity,
				EntityID:    "1",
				UserID:      "attacker",
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

	adminCtx := graph.WithClaims(context.Background(), &graph.AuthClaims{
		UserID: "admin-user",
		Role:   "admin",
	})

	mID := uuid.New().String()
	// Create an 'update' mutation targeting TopicMemory where BeforeJSON tries to set a non-existent column,
	// triggering an SQL statement failure during rollback
	mLog := models.MutationLog{
		ID:          mID,
		Topic:       "memory",
		Action:      "update",
		EntityType:  "TopicMemory",
		EntityID:    "1",
		UserID:      "user_test",
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
	betaClaims := &graph.AuthClaims{
		UserID:      "user_beta",
		FamilyGroup: "family_beta",
		Role:        "family",
	}
	betaCtx := graph.WithClaims(context.Background(), betaClaims)

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
