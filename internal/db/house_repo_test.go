package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHouseRepo_Contracts_CRUDAndIsolation(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewHouseRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()

	expDate := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Hour)

	// 1. Create contract for Family A
	contractA := models.HouseContractRecord{
		FamilyGroup:             famA,
		RecordType:              models.HouseRecordLease,
		Title:                   "Apartment Lease",
		VendorOrLandlord:        "Oakridge Properties",
		Amount:                  2200.0,
		Currency:                "USD",
		ExpirationOrRenewalDate: &expDate,
		NoticePeriodDays:        30,
		CreatedByUserID:         userA,
	}
	require.NoError(t, repo.CreateContract(scopeFor(t, client, famA), &contractA))
	require.NotZero(t, contractA.ID)

	// 2. Create contract for Family B
	contractB := models.HouseContractRecord{
		FamilyGroup:      famB,
		RecordType:       models.HouseRecordInsurance,
		Title:            "Homeowners Insurance Policy",
		VendorOrLandlord: "State Farm",
		Amount:           1200.0,
		Currency:         "USD",
	}
	require.NoError(t, repo.CreateContract(scopeFor(t, client, famB), &contractB))
	require.NotZero(t, contractB.ID)

	// 3. Positive lookup and isolation
	gotA, err := repo.GetContract(scopeFor(t, client, famA), contractA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Apartment Lease", gotA.Title)
	assert.Equal(t, famA, gotA.FamilyGroup)

	// Cross-tenant lookup: Family B querying contract A returns nil
	gotLeak, err := repo.GetContract(scopeFor(t, client, famB), contractA.ID)
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Family B must not access Family A's contract")

	// 4. ListContracts isolation
	listA, err := repo.ListContracts(scopeFor(t, client, famA), "all", nil)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, contractA.ID, listA[0].ID)

	listB, err := repo.ListContracts(scopeFor(t, client, famB), "all", nil)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, contractB.ID, listB[0].ID)

	// ListExpiringContracts
	expiringA, err := repo.ListExpiringContracts(scopeFor(t, client, famA), 60)
	require.NoError(t, err)
	require.Len(t, expiringA, 1)
	assert.Equal(t, contractA.ID, expiringA[0].ID)

	expiringB, err := repo.ListExpiringContracts(scopeFor(t, client, famB), 60)
	require.NoError(t, err)
	assert.Empty(t, expiringB)

	// 5. UpdateContract
	contractA.Amount = 2300.0
	contractA.Title = "Renewed Apartment Lease"
	require.NoError(t, repo.UpdateContract(scopeFor(t, client, famA), &contractA))

	recheckA, err := repo.GetContract(scopeFor(t, client, famA), contractA.ID)
	require.NoError(t, err)
	require.NotNil(t, recheckA)
	assert.Equal(t, 2300.0, recheckA.Amount)
	assert.Equal(t, "Renewed Apartment Lease", recheckA.Title)

	// 6. DeleteContract
	require.NoError(t, repo.DeleteContract(scopeFor(t, client, famA), contractA.ID))
	deletedA, err := repo.GetContract(scopeFor(t, client, famA), contractA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}

func TestHouseRepo_Maintenance_CRUDAndCompletion(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewHouseRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()
	userA := "usr-" + uuid.New().String()

	pastDue := time.Now().Add(-5 * 24 * time.Hour).Truncate(time.Hour)
	futureDue := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Hour)

	// 1. Create maintenance item for Family A (overdue)
	itemA := models.HomeMaintenanceItem{
		FamilyGroup:    famA,
		TaskName:       "Replace Furnace Filter",
		Category:       models.MaintenanceHVAC,
		IntervalDays:   90,
		NextDueAt:      pastDue,
		ContractorInfo: "Self or HVAC tech",
	}
	require.NoError(t, repo.CreateMaintenanceItem(scopeFor(t, client, famA), &itemA))
	require.NotZero(t, itemA.ID)

	// 2. Create maintenance item for Family B
	itemB := models.HomeMaintenanceItem{
		FamilyGroup:  famB,
		TaskName:     "Flush Water Heater",
		Category:     models.MaintenancePlumbing,
		IntervalDays: 365,
		NextDueAt:    futureDue,
	}
	require.NoError(t, repo.CreateMaintenanceItem(scopeFor(t, client, famB), &itemB))
	require.NotZero(t, itemB.ID)

	// 3. Positive read and isolation
	gotA, err := repo.GetMaintenanceItem(scopeFor(t, client, famA), itemA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Replace Furnace Filter", gotA.TaskName)

	gotLeak, err := repo.GetMaintenanceItem(scopeFor(t, client, famB), itemA.ID)
	require.NoError(t, err)
	assert.Nil(t, gotLeak, "Family B must not access Family A's maintenance item")

	// 4. Overdue filtering
	overdueA, err := repo.ListMaintenanceItems(scopeFor(t, client, famA), "all", true)
	require.NoError(t, err)
	require.Len(t, overdueA, 1)
	assert.Equal(t, itemA.ID, overdueA[0].ID)

	overdueB, err := repo.ListMaintenanceItems(scopeFor(t, client, famB), "all", true)
	require.NoError(t, err)
	assert.Empty(t, overdueB, "Family B item is in future, not overdue")

	// 5. Complete maintenance item
	completedTime := time.Now()
	completedItem, err := repo.CompleteMaintenanceItem(scopeFor(t, client, famA), itemA.ID, completedTime, userA)
	require.NoError(t, err)
	require.NotNil(t, completedItem)
	assert.NotNil(t, completedItem.LastCompletedAt)
	assert.Equal(t, userA, completedItem.UpdatedByUserID)
	// NextDueAt should have advanced by ~90 days
	assert.True(t, completedItem.NextDueAt.After(completedTime.Add(80*24*time.Hour)))

	// After completion, item is no longer overdue
	overdueAfter, err := repo.ListMaintenanceItems(scopeFor(t, client, famA), "all", true)
	require.NoError(t, err)
	assert.Empty(t, overdueAfter)

	// 6. Delete maintenance item
	require.NoError(t, repo.DeleteMaintenanceItem(scopeFor(t, client, famA), itemA.ID))
	deletedA, err := repo.GetMaintenanceItem(scopeFor(t, client, famA), itemA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}

func TestHouseRepo_VehicleAssets_CRUDAndMileage(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewHouseRepository(client.DB)

	famA := "fam-" + uuid.New().String()
	famB := "fam-" + uuid.New().String()

	// 1. Create vehicle for Family A
	vA := models.VehicleAsset{
		FamilyGroup:          famA,
		Name:                 "Toyota RAV4",
		LicensePlate:         "ABC-1234",
		CurrentMileageKm:     45000,
		NextServiceMileageKm: 50000,
	}
	require.NoError(t, repo.CreateVehicleAsset(scopeFor(t, client, famA), &vA))
	require.NotZero(t, vA.ID)

	// 2. Create vehicle for Family B
	vB := models.VehicleAsset{
		FamilyGroup:          famB,
		Name:                 "Honda Civic",
		LicensePlate:         "XYZ-9876",
		CurrentMileageKm:     25000,
		NextServiceMileageKm: 30000,
	}
	require.NoError(t, repo.CreateVehicleAsset(scopeFor(t, client, famB), &vB))
	require.NotZero(t, vB.ID)

	// 3. Positive read and isolation
	gotA, err := repo.GetVehicleAsset(scopeFor(t, client, famA), vA.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "Toyota RAV4", gotA.Name)

	listA, err := repo.ListVehicleAssets(scopeFor(t, client, famA))
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, "Toyota RAV4", listA[0].Name)

	listB, err := repo.ListVehicleAssets(scopeFor(t, client, famB))
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, "Honda Civic", listB[0].Name)

	// Service due check before mileage update
	assert.False(t, vA.IsServiceDue(), "45000km < 50000km, service not due yet")

	// 4. Update mileage past service threshold
	updatedV, err := repo.UpdateVehicleMileage(scopeFor(t, client, famA), vA.ID, 52000)
	require.NoError(t, err)
	require.NotNil(t, updatedV)
	assert.Equal(t, 52000, updatedV.CurrentMileageKm)
	assert.True(t, updatedV.IsServiceDue(), "52000km >= 50000km, service is now due")

	// 5. Delete vehicle asset
	require.NoError(t, repo.DeleteVehicleAsset(scopeFor(t, client, famA), vA.ID))
	deletedA, err := repo.GetVehicleAsset(scopeFor(t, client, famA), vA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedA)
}
