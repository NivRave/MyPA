package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurabilityRepo_Credentials(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	credA := models.EncryptedCredential{
		UserID:     userA,
		Provider:   "google_oauth",
		Ciphertext: []byte("encrypted-token-a-bytes"),
		UpdatedAt:  time.Now(),
	}
	err := client.SaveEncryptedCredential(credA)
	require.NoError(t, err)

	// Fetch userA credential
	gotA, err := client.GetEncryptedCredential(userA, "google_oauth")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, []byte("encrypted-token-a-bytes"), gotA.Ciphertext)

	// Verify isolation: userB cannot see userA's credential
	gotB, err := client.GetEncryptedCredential(userB, "google_oauth")
	assert.Error(t, err, "userB credential should not exist")
	assert.Nil(t, gotB)
}

func TestDurabilityRepo_MutationLogs_PositiveAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	tenantAUser := "usr-" + uuid.New().String()
	tenantAFamily := "fam-" + uuid.New().String()

	tenantBUser := "usr-" + uuid.New().String()
	tenantBFamily := "fam-" + uuid.New().String()

	// 1. Insert mutation logs for Tenant A
	logA1 := models.MutationLog{
		ID:          uuid.New().String(),
		UserID:      tenantAUser,
		FamilyGroup: tenantAFamily,
		Topic:       "baby",
		Action:      "create",
		EntityType:  "baby_care_log",
		EntityID:    "101",
		Summary:     "Feeding 120ml",
		CreatedAt:   time.Now().Add(-2 * time.Minute),
	}
	require.NoError(t, client.InsertMutationLog(logA1))

	logA2 := models.MutationLog{
		ID:          uuid.New().String(),
		UserID:      tenantAUser,
		FamilyGroup: tenantAFamily,
		Topic:       "house",
		Action:      "create",
		EntityType:  "house_contract_record",
		EntityID:    "102",
		Summary:     "Lease contract",
		CreatedAt:   time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, client.InsertMutationLog(logA2))

	// 2. Insert mutation log for Tenant B
	logB := models.MutationLog{
		ID:          uuid.New().String(),
		UserID:      tenantBUser,
		FamilyGroup: tenantBFamily,
		Topic:       "finance",
		Action:      "create",
		EntityType:  "financial_transaction",
		EntityID:    "201",
		Summary:     "Grocery store $50",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, client.InsertMutationLog(logB))

	// 3. Query Tenant A logs -> should return 2 logs, zero Tenant B data
	logsA, err := client.GetMutationLogsByTopic(tenantAFamily, tenantAUser, "all", 50)
	require.NoError(t, err)
	require.Len(t, logsA, 2)
	for _, l := range logsA {
		assert.Equal(t, tenantAFamily, l.FamilyGroup)
		assert.NotEqual(t, tenantBFamily, l.FamilyGroup)
		assert.NotEqual(t, "finance", l.Topic)
	}

	// Filter Tenant A by topic "baby"
	babyLogsA, err := client.GetMutationLogsByTopic(tenantAFamily, tenantAUser, "baby", 50)
	require.NoError(t, err)
	require.Len(t, babyLogsA, 1)
	assert.Equal(t, logA1.ID, babyLogsA[0].ID)

	// 4. Query Tenant B logs -> should return 1 log, zero Tenant A data
	logsB, err := client.GetMutationLogsByTopic(tenantBFamily, tenantBUser, "all", 50)
	require.NoError(t, err)
	require.Len(t, logsB, 1)
	assert.Equal(t, logB.ID, logsB[0].ID)

	// 5. GetMutationLogByID
	fetchedA1, err := client.GetMutationLogByID(logA1.ID)
	require.NoError(t, err)
	assert.Equal(t, logA1.Summary, fetchedA1.Summary)

	// 6. GetLastMutation & MarkMutationUndone
	lastMutA, err := client.GetLastMutation(tenantAUser, tenantAFamily)
	require.NoError(t, err)
	require.NotNil(t, lastMutA)
	assert.Equal(t, logA2.ID, lastMutA.ID) // Most recent active is logA2

	// Mark logA2 undone
	err = client.MarkMutationUndone(logA2.ID)
	require.NoError(t, err)

	// Verify logA2 is now marked undone
	checkUndone, err := client.GetMutationLogByID(logA2.ID)
	require.NoError(t, err)
	assert.True(t, checkUndone.IsUndone)

	// Now GetLastMutation should return logA1 (since logA2 is undone)
	newLastMutA, err := client.GetLastMutation(tenantAUser, tenantAFamily)
	require.NoError(t, err)
	require.NotNil(t, newLastMutA)
	assert.Equal(t, logA1.ID, newLastMutA.ID)
}

func TestDurabilityRepo_Idempotency(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	key := "idem-" + uuid.New().String()

	// Initial check: not found
	rec, err := client.CheckIdempotency(key)
	assert.Error(t, err)
	assert.Nil(t, rec)

	// Save idempotency token
	newRec := models.IdempotencyRecord{
		Key:             key,
		ResponseSummary: `{"status":"ok"}`,
		ExpiresAt:       time.Now().Add(1 * time.Hour),
	}
	err = client.SaveIdempotency(newRec)
	require.NoError(t, err)

	// Check again: found
	got, err := client.CheckIdempotency(key)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"status":"ok"}`, got.ResponseSummary)
}

func TestDurabilityRepo_ExportTopicData(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	user := "usr-" + uuid.New().String()
	family := "fam-" + uuid.New().String()

	log := models.MutationLog{
		ID:          uuid.New().String(),
		UserID:      user,
		FamilyGroup: family,
		Topic:       "baby",
		Action:      "create",
		EntityType:  "baby_profile",
		EntityID:    "1",
		Summary:     "Child profile created",
		CreatedAt:   time.Now(),
	}
	require.NoError(t, client.InsertMutationLog(log))

	// Export as JSON
	jsonStr, err := client.ExportTopicData(family, user, "baby", "json", 10)
	require.NoError(t, err)
	assert.Contains(t, jsonStr, "Child profile created")
	assert.Contains(t, jsonStr, family)

	// Export as CSV
	csvStr, err := client.ExportTopicData(family, user, "baby", "csv", 10)
	require.NoError(t, err)
	assert.Contains(t, csvStr, "id,timestamp,user_id,family_group,topic,action,entity_type,entity_id,summary,is_undone")
	assert.Contains(t, csvStr, "Child profile created")
}

func TestDurabilityRepo_RollbackSnapshot(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Test rollback of "create": table entity should be deleted
	user := models.User{
		ID:         uuid.New().String(),
		PlatformID: "tg-" + uuid.New().String(),
		Role:       "member",
	}
	require.NoError(t, client.DB.Create(&user).Error)

	mutCreate := models.MutationLog{
		ID:         uuid.New().String(),
		Action:     "create",
		EntityType: "user",
		EntityID:   user.ID,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, client.InsertMutationLog(mutCreate))

	err := client.RollbackSnapshot(ctx, mutCreate, "users")
	require.NoError(t, err)

	// Verify user deleted
	var checkUser models.User
	err = client.DB.Where("id = ?", user.ID).First(&checkUser).Error
	assert.Error(t, err, "user should have been deleted by rollback")

	// Verify mutation marked undone
	mutAfter, err := client.GetMutationLogByID(mutCreate.ID)
	require.NoError(t, err)
	assert.True(t, mutAfter.IsUndone)

	// 2. Test rollback of "update": table entity should revert to BeforeJSON
	user2 := models.User{
		ID:         uuid.New().String(),
		PlatformID: "tg-" + uuid.New().String(),
		Role:       "admin", // Updated value
	}
	require.NoError(t, client.DB.Create(&user2).Error)

	beforeMap := map[string]interface{}{
		"role": "member", // Original value before update
	}
	beforeJSON, _ := json.Marshal(beforeMap)

	mutUpdate := models.MutationLog{
		ID:         uuid.New().String(),
		Action:     "update",
		EntityType: "user",
		EntityID:   user2.ID,
		BeforeJSON: string(beforeJSON),
		CreatedAt:  time.Now(),
	}
	require.NoError(t, client.InsertMutationLog(mutUpdate))

	err = client.RollbackSnapshot(ctx, mutUpdate, "users")
	require.NoError(t, err)

	var checkUser2 models.User
	require.NoError(t, client.DB.Where("id = ?", user2.ID).First(&checkUser2).Error)
	assert.Equal(t, "member", checkUser2.Role, "role should have rolled back to member")
}
