package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedRepo_SharedSpaces_PositiveAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewSharedRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	// 1. Upsert shared spaces for Family A
	spaceA1 := models.SharedSpace{
		FamilyGroup:      famA,
		Topic:            "baby",
		OwnerUserID:      userA,
		SharedWithUserID: "*",
		CanView:          true,
		CanEdit:          true,
		NotifyMode:       "immediate",
	}
	require.NoError(t, repo.UpsertSharedSpace(&spaceA1))
	require.NotZero(t, spaceA1.ID)

	spaceA2 := models.SharedSpace{
		FamilyGroup:      famA,
		Topic:            "house",
		OwnerUserID:      userA,
		SharedWithUserID: "*",
		CanView:          true,
		CanEdit:          false,
		NotifyMode:       "daily",
	}
	require.NoError(t, repo.UpsertSharedSpace(&spaceA2))
	require.NotZero(t, spaceA2.ID)

	// 2. Upsert shared space for Family B
	spaceB := models.SharedSpace{
		FamilyGroup:      famB,
		Topic:            "finance",
		OwnerUserID:      userB,
		SharedWithUserID: "*",
		CanView:          true,
		CanEdit:          true,
		NotifyMode:       "digest",
	}
	require.NoError(t, repo.UpsertSharedSpace(&spaceB))
	require.NotZero(t, spaceB.ID)

	// 3. Positive verification and cross-tenant isolation
	spacesA, err := repo.GetSharedSpacesForFamily(famA)
	require.NoError(t, err)
	require.Len(t, spacesA, 2)
	assert.Equal(t, "baby", spacesA[0].Topic)
	assert.Equal(t, "house", spacesA[1].Topic)

	spacesB, err := repo.GetSharedSpacesForFamily(famB)
	require.NoError(t, err)
	require.Len(t, spacesB, 1)
	assert.Equal(t, "finance", spacesB[0].Topic)

	// Direct lookup by topic
	gotA1, err := repo.GetSharedSpace(famA, "baby")
	require.NoError(t, err)
	require.NotNil(t, gotA1)
	assert.Equal(t, "immediate", gotA1.NotifyMode)

	// Family B cannot see Family A's space
	gotWrong, err := repo.GetSharedSpace(famB, "baby")
	require.NoError(t, err)
	assert.Nil(t, gotWrong, "Family B should not see Family A's baby space")

	// 4. Update Family A space and verify in-place update (ID preserved)
	updatedSpaceA1 := models.SharedSpace{
		FamilyGroup:      famA,
		Topic:            "baby",
		OwnerUserID:      userA,
		SharedWithUserID: "*",
		CanView:          true,
		CanEdit:          false, // flipped to false
		NotifyMode:       "muted",
	}
	require.NoError(t, repo.UpsertSharedSpace(&updatedSpaceA1))
	assert.Equal(t, spaceA1.ID, updatedSpaceA1.ID)

	recheckA1, err := repo.GetSharedSpace(famA, "baby")
	require.NoError(t, err)
	require.NotNil(t, recheckA1)
	assert.False(t, recheckA1.CanEdit)
	assert.Equal(t, "muted", recheckA1.NotifyMode)
}

func TestSharedRepo_SharedReminders_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewSharedRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()
	userB := "usr-" + uuid.New().String()

	dueTime := time.Now().Add(2 * time.Hour)

	// 1. Create reminder for Family A
	remA := models.SharedTaskReminder{
		FamilyGroup:     famA,
		Topic:           "baby",
		Title:           "Buy vitamin D drops",
		DueTime:         &dueTime,
		CreatedByUserID: userA,
	}
	require.NoError(t, repo.CreateSharedReminder(&remA))
	require.NotZero(t, remA.ID)

	// 2. Create reminder for Family B
	remB := models.SharedTaskReminder{
		FamilyGroup:     famB,
		Topic:           "finance",
		Title:           "File quarterly taxes",
		DueTime:         &dueTime,
		CreatedByUserID: userB,
	}
	require.NoError(t, repo.CreateSharedReminder(&remB))
	require.NotZero(t, remB.ID)

	// 3. Verify GetSharedReminder with tenant scoping
	gotA, err := repo.GetSharedReminder(remA.ID, famA)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Buy vitamin D drops", gotA.Title)

	// Cross-tenant scoping: Family B querying Family A's reminder ID returns nil
	gotLeak, err := repo.GetSharedReminder(remA.ID, famB)
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Family B should not access Family A's reminder")

	// 4. Verify ListSharedReminders isolation
	listA, err := repo.ListSharedReminders(famA, "all", false)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, remA.ID, listA[0].ID)

	listB, err := repo.ListSharedReminders(famB, "all", false)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, remB.ID, listB[0].ID)

	// 5. Complete reminder for Family A
	completed, err := repo.CompleteSharedReminder(remA.ID, userA, famA)
	require.NoError(t, err)
	require.NotNil(t, completed)
	assert.True(t, completed.IsCompleted)
	assert.Equal(t, userA, completed.CompletedByUserID)
	assert.NotNil(t, completed.CompletedAt)

	// After completion, incomplete list should be empty
	listAActive, err := repo.ListSharedReminders(famA, "all", false)
	require.NoError(t, err)
	assert.Empty(t, listAActive)

	// When includeCompleted is true, it should appear
	listAAll, err := repo.ListSharedReminders(famA, "all", true)
	require.NoError(t, err)
	require.Len(t, listAAll, 1)
	assert.True(t, listAAll[0].IsCompleted)
}

func TestSharedRepo_DueReminders(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewSharedRepository(client.DB)

	fam := "fam-" + uuid.New().String()
	user := "usr-" + uuid.New().String()

	pastDue := time.Now().Add(-15 * time.Minute)
	futureDue := time.Now().Add(15 * time.Minute)

	// Reminder 1: past due
	rem1 := models.SharedTaskReminder{
		FamilyGroup:     fam,
		Topic:           "house",
		Title:           "Change AC filter",
		DueTime:         &pastDue,
		CreatedByUserID: user,
		IsCompleted:     false,
		IsSent:          false,
	}
	require.NoError(t, repo.CreateSharedReminder(&rem1))

	// Reminder 2: future due
	rem2 := models.SharedTaskReminder{
		FamilyGroup:     fam,
		Topic:           "house",
		Title:           "Clean gutters",
		DueTime:         &futureDue,
		CreatedByUserID: user,
		IsCompleted:     false,
		IsSent:          false,
	}
	require.NoError(t, repo.CreateSharedReminder(&rem2))

	// Fetch due
	dueReminders, err := repo.GetDueSharedReminders()
	require.NoError(t, err)
	require.Len(t, dueReminders, 1)
	assert.Equal(t, rem1.ID, dueReminders[0].ID)

	// Mark sent
	err = repo.MarkSharedReminderSent(rem1.ID)
	require.NoError(t, err)

	// Recheck due: should now be empty
	dueAfter, err := repo.GetDueSharedReminders()
	require.NoError(t, err)
	assert.Empty(t, dueAfter)
}
