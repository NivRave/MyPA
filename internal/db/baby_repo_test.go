package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBabyRepo_Profile_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewBabyRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	dob := time.Now().Add(-60 * 24 * time.Hour).Truncate(time.Hour)

	// 1. Create Profile for Family A
	profileA := models.BabyProfile{
		FamilyGroup:        famA,
		Name:               "Alice",
		DateOfBirth:        dob,
		Gender:             "female",
		BirthWeightGrams:   3400,
		CurrentWeightGrams: 4200,
		HeightCm:           56.5,
		PediatricianInfo:   "Dr. Smith, City Clinic",
		AllergiesNotes:     "None known",
	}
	require.NoError(t, repo.UpsertBabyProfile(&profileA))
	require.NotZero(t, profileA.ID)

	// 2. Create Profile for Family B
	profileB := models.BabyProfile{
		FamilyGroup:        famB,
		Name:               "Bob",
		DateOfBirth:        dob,
		Gender:             "male",
		BirthWeightGrams:   3600,
		CurrentWeightGrams: 4500,
		HeightCm:           58.0,
	}
	require.NoError(t, repo.UpsertBabyProfile(&profileB))
	require.NotZero(t, profileB.ID)

	// 3. Positive lookup and cross-tenant isolation
	gotA, err := repo.GetBabyProfile(profileA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Alice", gotA.Name)
	assert.Equal(t, famA, gotA.FamilyGroup)

	byNameA, err := repo.GetBabyProfileByName(famA, "alice")
	require.NoError(t, err)
	require.NotNil(t, byNameA)
	assert.Equal(t, profileA.ID, byNameA.ID)

	// Cross-tenant: Family A querying Bob returns nil
	byNameLeak, err := repo.GetBabyProfileByName(famA, "bob")
	require.NoError(t, err)
	assert.Nil(t, byNameLeak, "Family A must not find Family B's child profile")

	// GetBabyProfilesForFamily isolation
	listA, err := repo.GetBabyProfilesForFamily(famA)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, "Alice", listA[0].Name)

	listB, err := repo.GetBabyProfilesForFamily(famB)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, "Bob", listB[0].Name)

	// 4. Update profile in-place
	profileA.CurrentWeightGrams = 4600
	profileA.AllergiesNotes = "Slight eczema noticed"
	require.NoError(t, repo.UpsertBabyProfile(&profileA))

	recheckA, err := repo.GetBabyProfile(profileA.ID)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, 4600, recheckA.CurrentWeightGrams)
	assert.Equal(t, "Slight eczema noticed", recheckA.AllergiesNotes)

	// 5. Delete profile
	require.NoError(t, repo.DeleteBabyProfile(profileA.ID))
	deletedA, err := repo.GetBabyProfile(profileA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}

func TestBabyRepo_CareLogs_CRUDAnalyticsAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewBabyRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()

	// Create Baby Profile
	babyA := models.BabyProfile{
		FamilyGroup: famA,
		Name:        "Charlie",
		DateOfBirth: time.Now().Add(-30 * 24 * time.Hour),
	}
	require.NoError(t, repo.UpsertBabyProfile(&babyA))

	now := time.Now()

	// 1. Create logs for Family A
	logFeed := models.BabyCareLog{
		BabyID:         babyA.ID,
		FamilyGroup:    famA,
		LogType:        "feeding",
		ValueNumeric:   120,
		Unit:           "ml",
		Details:        "Formula milk",
		Timestamp:      now.Add(-2 * time.Hour),
		LoggedByUserID: userA,
	}
	require.NoError(t, repo.CreateCareLog(&logFeed))
	require.NotZero(t, logFeed.ID)

	logDiaper := models.BabyCareLog{
		BabyID:         babyA.ID,
		FamilyGroup:    famA,
		LogType:        "diaper",
		Details:        "wet",
		Timestamp:      now.Add(-1 * time.Hour),
		LoggedByUserID: userA,
	}
	require.NoError(t, repo.CreateCareLog(&logDiaper))

	logMed := models.BabyCareLog{
		BabyID:         babyA.ID,
		FamilyGroup:    famA,
		LogType:        "medication",
		ValueNumeric:   2.5,
		Unit:           "ml",
		Details:        "Infant Tylenol 160mg/5ml",
		Timestamp:      now.Add(-30 * time.Minute),
		LoggedByUserID: userA,
	}
	require.NoError(t, repo.CreateCareLog(&logMed))

	// 2. Query and filter logs
	allLogsA, err := repo.GetCareLogs(babyA.ID, famA, "", now.Add(-5*time.Hour), now.Add(1*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, allLogsA, 3)

	feedLogsA, err := repo.GetCareLogs(babyA.ID, famA, "feeding", time.Time{}, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, feedLogsA, 1)
	assert.Equal(t, 120.0, feedLogsA[0].ValueNumeric)

	// Cross-tenant isolation: querying with Family B returns 0
	leakLogs, err := repo.GetCareLogs(babyA.ID, famB, "", time.Time{}, time.Time{}, 10)
	require.NoError(t, err)
	assert.Empty(t, leakLogs, "Family B must not see Family A's baby care logs")

	// 3. GetLastMedicationLog
	lastMed, err := repo.GetLastMedicationLog(babyA.ID, famA, "Tylenol")
	require.NoError(t, err)
	require.NotNil(t, lastMed)
	assert.Contains(t, lastMed.Details, "Tylenol")
	assert.Equal(t, 2.5, lastMed.ValueNumeric)

	// Cross-tenant check for medication
	wrongFamMed, err := repo.GetLastMedicationLog(babyA.ID, famB, "Tylenol")
	require.NoError(t, err)
	assert.Nil(t, wrongFamMed)

	// 4. GetCareStatsSummary
	summary, err := repo.GetCareStatsSummary(babyA.ID, famA, now.Add(-5*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, 1, summary.TotalFeedings)
	assert.Equal(t, 120.0, summary.TotalFeedingVolume)
	assert.Equal(t, 1, summary.WetDiapers)
	assert.NotNil(t, summary.LastMedication)
	assert.Equal(t, logMed.ID, summary.LastMedication.ID)

	// 5. DeleteCareLog
	require.NoError(t, repo.DeleteCareLog(logFeed.ID))
	gotDeleted, err := repo.GetCareLogByID(logFeed.ID)
	require.NoError(t, err)
	assert.Nil(t, gotDeleted)
}

func TestBabyRepo_Inventory_CRUDAndLowStock(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewBabyRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	// 1. Create inventory items for Family A
	itemA1 := models.BabyInventoryItem{
		FamilyGroup:       famA,
		ItemName:          "Formula Powder",
		QuantityRemaining: 1.0,
		Unit:              "canister",
		LowThreshold:      2.0, // Low stock!
	}
	require.NoError(t, repo.UpsertBabyInventoryItem(&itemA1))
	require.NotZero(t, itemA1.ID)

	itemA2 := models.BabyInventoryItem{
		FamilyGroup:       famA,
		ItemName:          "Diapers Size 1",
		QuantityRemaining: 80.0,
		Unit:              "count",
		LowThreshold:      20.0, // Sufficient
	}
	require.NoError(t, repo.UpsertBabyInventoryItem(&itemA2))
	require.NotZero(t, itemA2.ID)

	// 2. Create inventory item for Family B
	itemB := models.BabyInventoryItem{
		FamilyGroup:       famB,
		ItemName:          "Baby Wipes",
		QuantityRemaining: 10.0,
		Unit:              "packs",
		LowThreshold:      3.0,
	}
	require.NoError(t, repo.UpsertBabyInventoryItem(&itemB))
	require.NotZero(t, itemB.ID)

	// 3. Positive lookup and isolation
	listA, err := repo.ListBabyInventoryItems(famA)
	require.NoError(t, err)
	require.Len(t, listA, 2)

	listB, err := repo.ListBabyInventoryItems(famB)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, "Baby Wipes", listB[0].ItemName)

	byNameA, err := repo.GetBabyInventoryItemByName(famA, "formula powder")
	require.NoError(t, err)
	require.NotNil(t, byNameA)
	assert.Equal(t, itemA1.ID, byNameA.ID)

	// Cross-tenant: Family A querying Baby Wipes returns nil
	byNameLeak, err := repo.GetBabyInventoryItemByName(famA, "baby wipes")
	require.NoError(t, err)
	assert.Nil(t, byNameLeak)

	// 4. Low stock detection
	lowA, err := repo.GetLowBabyInventoryItems(famA)
	require.NoError(t, err)
	require.Len(t, lowA, 1)
	assert.Equal(t, "Formula Powder", lowA[0].ItemName)

	// 5. Restock via upsert
	itemA1.QuantityRemaining = 5.0
	require.NoError(t, repo.UpsertBabyInventoryItem(&itemA1))

	lowAfterRestock, err := repo.GetLowBabyInventoryItems(famA)
	require.NoError(t, err)
	assert.Empty(t, lowAfterRestock, "Item should no longer be low stock after replenishment")

	// 6. Delete inventory item
	require.NoError(t, repo.DeleteBabyInventoryItem(itemA2.ID))
	gotDeleted, err := repo.GetBabyInventoryItem(itemA2.ID)
	require.NoError(t, err)
	assert.Nil(t, gotDeleted)
}
