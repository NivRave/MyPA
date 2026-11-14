package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(
		&models.HouseContractRecord{},
		&models.HomeMaintenanceItem{},
		&models.VehicleAsset{},
	)
}

// HouseRepository defines persistence operations for house contracts/receipts,
// preventive home maintenance schedules, and family vehicle assets.
type HouseRepository interface {
	// Contracts, Leases, Warranties & Receipts
	CreateContract(c *models.HouseContractRecord) error
	GetContract(id uint, familyGroup ...string) (*models.HouseContractRecord, error)
	UpdateContract(c *models.HouseContractRecord) error
	DeleteContract(id uint) error
	ListContracts(familyGroup string, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error)
	ListExpiringContracts(familyGroup string, withinDays int) ([]models.HouseContractRecord, error)

	// Home Maintenance Items
	CreateMaintenanceItem(m *models.HomeMaintenanceItem) error
	GetMaintenanceItem(id uint, familyGroup ...string) (*models.HomeMaintenanceItem, error)
	UpdateMaintenanceItem(m *models.HomeMaintenanceItem) error
	DeleteMaintenanceItem(id uint) error
	ListMaintenanceItems(familyGroup string, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error)
	CompleteMaintenanceItem(id uint, completedAt time.Time, userID string, familyGroup ...string) (*models.HomeMaintenanceItem, error)

	// Vehicle Assets
	CreateVehicleAsset(v *models.VehicleAsset) error
	GetVehicleAsset(id uint) (*models.VehicleAsset, error)
	UpdateVehicleAsset(v *models.VehicleAsset) error
	DeleteVehicleAsset(id uint) error
	ListVehicleAssets(familyGroup string) ([]models.VehicleAsset, error)
	UpdateVehicleMileage(id uint, mileageKm int) (*models.VehicleAsset, error)
}

// NewHouseRepository creates a HouseRepository instance backed by the given gorm.DB.
func NewHouseRepository(database *gorm.DB) HouseRepository {
	return &Client{DB: database}
}

// CreateContract inserts a new house contract, warranty, or receipt record.
func (c *Client) CreateContract(contract *models.HouseContractRecord) error {
	if contract == nil {
		return fmt.Errorf("contract cannot be nil")
	}
	if contract.CreatedAt.IsZero() {
		contract.CreatedAt = time.Now()
	}
	contract.RecordType = strings.ToLower(strings.TrimSpace(contract.RecordType))
	if contract.KeyTermsJSON == "" {
		contract.KeyTermsJSON = "{}"
	}

	result := c.DB.Create(contract)
	if result.Error != nil {
		return fmt.Errorf("failed to create house contract: %w", result.Error)
	}
	return nil
}

// GetContract retrieves a house contract record by ID, optionally scoped to tenant. Returns (nil, nil) if not found.
func (c *Client) GetContract(id uint, familyGroup ...string) (*models.HouseContractRecord, error) {
	var record models.HouseContractRecord
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR created_by_user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.First(&record)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get house contract %d: %w", id, result.Error)
	}
	return &record, nil
}

// UpdateContract saves changes to an existing house contract record.
func (c *Client) UpdateContract(contract *models.HouseContractRecord) error {
	if contract == nil {
		return fmt.Errorf("contract cannot be nil")
	}
	contract.RecordType = strings.ToLower(strings.TrimSpace(contract.RecordType))
	result := c.DB.Save(contract)
	if result.Error != nil {
		return fmt.Errorf("failed to update house contract %d: %w", contract.ID, result.Error)
	}
	return nil
}

// DeleteContract removes a house contract record by ID.
func (c *Client) DeleteContract(id uint) error {
	result := c.DB.Delete(&models.HouseContractRecord{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete house contract %d: %w", id, result.Error)
	}
	return nil
}

// ListContracts retrieves house contracts matching the given family group and filters.
func (c *Client) ListContracts(familyGroup string, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error) {
	query := c.DB.Model(&models.HouseContractRecord{})
	if familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR created_by_user_id = ?)", familyGroup, familyGroup)
	}
	recordType = strings.ToLower(strings.TrimSpace(recordType))
	if recordType != "" && recordType != "all" {
		query = query.Where("record_type = ?", recordType)
	}
	if expiringBefore != nil {
		query = query.Where("expiration_or_renewal_date IS NOT NULL AND expiration_or_renewal_date <= ?", *expiringBefore)
	}

	var records []models.HouseContractRecord
	result := query.Order("created_at DESC").Find(&records)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list house contracts: %w", result.Error)
	}
	return records, nil
}

// ListExpiringContracts queries contracts expiring between now and withinDays from now.
func (c *Client) ListExpiringContracts(familyGroup string, withinDays int) ([]models.HouseContractRecord, error) {
	now := time.Now()
	cutoff := now.AddDate(0, 0, withinDays)

	query := c.DB.Model(&models.HouseContractRecord{}).
		Where("expiration_or_renewal_date IS NOT NULL").
		Where("expiration_or_renewal_date >= ? AND expiration_or_renewal_date <= ?", now.Add(-24*time.Hour), cutoff)

	if familyGroup != "" {
		query = query.Where("family_group = ?", familyGroup)
	}

	var records []models.HouseContractRecord
	result := query.Order("expiration_or_renewal_date ASC").Find(&records)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list expiring contracts: %w", result.Error)
	}
	return records, nil
}

// CreateMaintenanceItem inserts a new preventive home maintenance task.
func (c *Client) CreateMaintenanceItem(item *models.HomeMaintenanceItem) error {
	if item == nil {
		return fmt.Errorf("maintenance item cannot be nil")
	}
	item.Category = strings.ToLower(strings.TrimSpace(item.Category))
	if item.NextDueAt.IsZero() {
		if item.IntervalDays > 0 {
			item.NextDueAt = time.Now().AddDate(0, 0, item.IntervalDays)
		} else {
			item.NextDueAt = time.Now()
		}
	}

	result := c.DB.Create(item)
	if result.Error != nil {
		return fmt.Errorf("failed to create home maintenance item: %w", result.Error)
	}
	return nil
}

// GetMaintenanceItem retrieves a maintenance item by ID, optionally scoped to tenant. Returns (nil, nil) if not found.
func (c *Client) GetMaintenanceItem(id uint, familyGroup ...string) (*models.HomeMaintenanceItem, error) {
	var item models.HomeMaintenanceItem
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR updated_by_user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.First(&item)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get maintenance item %d: %w", id, result.Error)
	}
	return &item, nil
}

// UpdateMaintenanceItem updates an existing maintenance item in the database.
func (c *Client) UpdateMaintenanceItem(item *models.HomeMaintenanceItem) error {
	if item == nil {
		return fmt.Errorf("maintenance item cannot be nil")
	}
	item.Category = strings.ToLower(strings.TrimSpace(item.Category))
	result := c.DB.Save(item)
	if result.Error != nil {
		return fmt.Errorf("failed to update maintenance item %d: %w", item.ID, result.Error)
	}
	return nil
}

// DeleteMaintenanceItem removes a maintenance item by ID.
func (c *Client) DeleteMaintenanceItem(id uint) error {
	result := c.DB.Delete(&models.HomeMaintenanceItem{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete maintenance item %d: %w", id, result.Error)
	}
	return nil
}

// ListMaintenanceItems retrieves home maintenance items matching the filters.
func (c *Client) ListMaintenanceItems(familyGroup string, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error) {
	query := c.DB.Model(&models.HomeMaintenanceItem{})
	if familyGroup != "" {
		query = query.Where("family_group = ?", familyGroup)
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category != "" && category != "all" {
		query = query.Where("category = ?", category)
	}
	if overdueOnly {
		query = query.Where("next_due_at <= ?", time.Now())
	}

	var items []models.HomeMaintenanceItem
	result := query.Order("next_due_at ASC").Find(&items)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list home maintenance items: %w", result.Error)
	}
	return items, nil
}

// CompleteMaintenanceItem records completion of a task, rolls NextDueAt forward, and saves it.
func (c *Client) CompleteMaintenanceItem(id uint, completedAt time.Time, userID string, familyGroup ...string) (*models.HomeMaintenanceItem, error) {
	item, err := c.GetMaintenanceItem(id, familyGroup...)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("home maintenance item %d not found", id)
	}

	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	item.MarkCompleted(completedAt, userID)

	if err := c.UpdateMaintenanceItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

// CreateVehicleAsset inserts a new vehicle asset record into the database.
func (c *Client) CreateVehicleAsset(vehicle *models.VehicleAsset) error {
	if vehicle == nil {
		return fmt.Errorf("vehicle asset cannot be nil")
	}
	result := c.DB.Create(vehicle)
	if result.Error != nil {
		return fmt.Errorf("failed to create vehicle asset: %w", result.Error)
	}
	return nil
}

// GetVehicleAsset retrieves a vehicle asset by ID. Returns (nil, nil) if not found.
func (c *Client) GetVehicleAsset(id uint) (*models.VehicleAsset, error) {
	var asset models.VehicleAsset
	result := c.DB.First(&asset, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get vehicle asset %d: %w", id, result.Error)
	}
	return &asset, nil
}

// UpdateVehicleAsset saves changes to an existing vehicle asset.
func (c *Client) UpdateVehicleAsset(vehicle *models.VehicleAsset) error {
	if vehicle == nil {
		return fmt.Errorf("vehicle asset cannot be nil")
	}
	result := c.DB.Save(vehicle)
	if result.Error != nil {
		return fmt.Errorf("failed to update vehicle asset %d: %w", vehicle.ID, result.Error)
	}
	return nil
}

// DeleteVehicleAsset removes a vehicle asset record by ID.
func (c *Client) DeleteVehicleAsset(id uint) error {
	result := c.DB.Delete(&models.VehicleAsset{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete vehicle asset %d: %w", id, result.Error)
	}
	return nil
}

// ListVehicleAssets retrieves all vehicle assets for a family group.
func (c *Client) ListVehicleAssets(familyGroup string) ([]models.VehicleAsset, error) {
	query := c.DB.Model(&models.VehicleAsset{})
	if familyGroup != "" {
		query = query.Where("family_group = ?", familyGroup)
	}

	var assets []models.VehicleAsset
	result := query.Order("name ASC").Find(&assets)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list vehicle assets: %w", result.Error)
	}
	return assets, nil
}

// UpdateVehicleMileage updates the current odometer reading of a vehicle asset.
func (c *Client) UpdateVehicleMileage(id uint, mileageKm int) (*models.VehicleAsset, error) {
	asset, err := c.GetVehicleAsset(id)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("vehicle asset %d not found", id)
	}

	asset.CurrentMileageKm = mileageKm
	if err := c.UpdateVehicleAsset(asset); err != nil {
		return nil, err
	}
	return asset, nil
}
