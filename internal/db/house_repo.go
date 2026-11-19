package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/identity"
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
// preventive home maintenance schedules, and family vehicle assets. Every method is confined to
// the scope's household (V4-30): reads filter by household_id, writes stamp it.
type HouseRepository interface {
	// Contracts, Leases, Warranties & Receipts
	CreateContract(s identity.Scope, c *models.HouseContractRecord) error
	GetContract(s identity.Scope, id uint) (*models.HouseContractRecord, error)
	UpdateContract(s identity.Scope, c *models.HouseContractRecord) error
	DeleteContract(s identity.Scope, id uint) error
	ListContracts(s identity.Scope, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error)
	ListExpiringContracts(s identity.Scope, withinDays int) ([]models.HouseContractRecord, error)

	// Home Maintenance Items
	CreateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error
	GetMaintenanceItem(s identity.Scope, id uint) (*models.HomeMaintenanceItem, error)
	UpdateMaintenanceItem(s identity.Scope, m *models.HomeMaintenanceItem) error
	DeleteMaintenanceItem(s identity.Scope, id uint) error
	ListMaintenanceItems(s identity.Scope, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error)
	CompleteMaintenanceItem(s identity.Scope, id uint, completedAt time.Time, userID string) (*models.HomeMaintenanceItem, error)

	// Vehicle Assets
	CreateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error
	GetVehicleAsset(s identity.Scope, id uint) (*models.VehicleAsset, error)
	UpdateVehicleAsset(s identity.Scope, v *models.VehicleAsset) error
	DeleteVehicleAsset(s identity.Scope, id uint) error
	ListVehicleAssets(s identity.Scope) ([]models.VehicleAsset, error)
	UpdateVehicleMileage(s identity.Scope, id uint, mileageKm int) (*models.VehicleAsset, error)
}

// NewHouseRepository creates a HouseRepository instance backed by the given gorm.DB.
func NewHouseRepository(database *gorm.DB) HouseRepository {
	return &Client{DB: database}
}

// CreateContract inserts a new house contract, warranty, or receipt record into the scope's household.
func (c *Client) CreateContract(s identity.Scope, contract *models.HouseContractRecord) error {
	if contract == nil {
		return fmt.Errorf("contract cannot be nil")
	}
	if err := stamp(s, &contract.HouseholdID, &contract.FamilyGroup); err != nil {
		return err
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

// GetContract retrieves a house contract record of the scope's household by ID. Returns (nil, nil) if not found.
func (c *Client) GetContract(s identity.Scope, id uint) (*models.HouseContractRecord, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var record models.HouseContractRecord
	result := q.Where("id = ?", id).First(&record)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get house contract %d: %w", id, result.Error)
	}
	return &record, nil
}

// UpdateContract saves changes to an existing house contract record of the scope's household.
func (c *Client) UpdateContract(s identity.Scope, contract *models.HouseContractRecord) error {
	if contract == nil {
		return fmt.Errorf("contract cannot be nil")
	}
	if err := stamp(s, &contract.HouseholdID, &contract.FamilyGroup); err != nil {
		return err
	}
	contract.RecordType = strings.ToLower(strings.TrimSpace(contract.RecordType))
	return writeBackInHousehold(c.DB, s, contract.ID, contract, "house contract")
}

// DeleteContract removes a house contract record of the scope's household by ID.
func (c *Client) DeleteContract(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.HouseContractRecord{}, id, "house contract")
}

// ListContracts retrieves the scope household's house contracts matching the filters.
func (c *Client) ListContracts(s identity.Scope, recordType string, expiringBefore *time.Time) ([]models.HouseContractRecord, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = query.Model(&models.HouseContractRecord{})
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

// ListExpiringContracts queries the scope household's contracts expiring between now and withinDays from now.
func (c *Client) ListExpiringContracts(s identity.Scope, withinDays int) ([]models.HouseContractRecord, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cutoff := now.AddDate(0, 0, withinDays)

	query = query.Model(&models.HouseContractRecord{}).
		Where("expiration_or_renewal_date IS NOT NULL").
		Where("expiration_or_renewal_date >= ? AND expiration_or_renewal_date <= ?", now.Add(-24*time.Hour), cutoff)

	var records []models.HouseContractRecord
	result := query.Order("expiration_or_renewal_date ASC").Find(&records)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list expiring contracts: %w", result.Error)
	}
	return records, nil
}

// CreateMaintenanceItem inserts a new preventive home maintenance task into the scope's household.
func (c *Client) CreateMaintenanceItem(s identity.Scope, item *models.HomeMaintenanceItem) error {
	if item == nil {
		return fmt.Errorf("maintenance item cannot be nil")
	}
	if err := stamp(s, &item.HouseholdID, &item.FamilyGroup); err != nil {
		return err
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

// GetMaintenanceItem retrieves a maintenance item of the scope's household by ID. Returns (nil, nil) if not found.
func (c *Client) GetMaintenanceItem(s identity.Scope, id uint) (*models.HomeMaintenanceItem, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var item models.HomeMaintenanceItem
	result := q.Where("id = ?", id).First(&item)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get maintenance item %d: %w", id, result.Error)
	}
	return &item, nil
}

// UpdateMaintenanceItem updates an existing maintenance item of the scope's household.
func (c *Client) UpdateMaintenanceItem(s identity.Scope, item *models.HomeMaintenanceItem) error {
	if item == nil {
		return fmt.Errorf("maintenance item cannot be nil")
	}
	if err := stamp(s, &item.HouseholdID, &item.FamilyGroup); err != nil {
		return err
	}
	item.Category = strings.ToLower(strings.TrimSpace(item.Category))
	return writeBackInHousehold(c.DB, s, item.ID, item, "maintenance item")
}

// DeleteMaintenanceItem removes a maintenance item of the scope's household by ID.
func (c *Client) DeleteMaintenanceItem(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.HomeMaintenanceItem{}, id, "maintenance item")
}

// ListMaintenanceItems retrieves the scope household's home maintenance items matching the filters.
func (c *Client) ListMaintenanceItems(s identity.Scope, category string, overdueOnly bool) ([]models.HomeMaintenanceItem, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = query.Model(&models.HomeMaintenanceItem{})
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
func (c *Client) CompleteMaintenanceItem(s identity.Scope, id uint, completedAt time.Time, userID string) (*models.HomeMaintenanceItem, error) {
	item, err := c.GetMaintenanceItem(s, id)
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

	if err := c.UpdateMaintenanceItem(s, item); err != nil {
		return nil, err
	}
	return item, nil
}

// CreateVehicleAsset inserts a new vehicle asset record into the scope's household.
func (c *Client) CreateVehicleAsset(s identity.Scope, vehicle *models.VehicleAsset) error {
	if vehicle == nil {
		return fmt.Errorf("vehicle asset cannot be nil")
	}
	if err := stamp(s, &vehicle.HouseholdID, &vehicle.FamilyGroup); err != nil {
		return err
	}
	result := c.DB.Create(vehicle)
	if result.Error != nil {
		return fmt.Errorf("failed to create vehicle asset: %w", result.Error)
	}
	return nil
}

// GetVehicleAsset retrieves a vehicle asset of the scope's household by ID. Returns (nil, nil) if not found.
func (c *Client) GetVehicleAsset(s identity.Scope, id uint) (*models.VehicleAsset, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var asset models.VehicleAsset
	result := q.Where("id = ?", id).First(&asset)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get vehicle asset %d: %w", id, result.Error)
	}
	return &asset, nil
}

// UpdateVehicleAsset saves changes to an existing vehicle asset of the scope's household.
func (c *Client) UpdateVehicleAsset(s identity.Scope, vehicle *models.VehicleAsset) error {
	if vehicle == nil {
		return fmt.Errorf("vehicle asset cannot be nil")
	}
	if err := stamp(s, &vehicle.HouseholdID, &vehicle.FamilyGroup); err != nil {
		return err
	}
	return writeBackInHousehold(c.DB, s, vehicle.ID, vehicle, "vehicle asset")
}

// DeleteVehicleAsset removes a vehicle asset record of the scope's household by ID.
func (c *Client) DeleteVehicleAsset(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.VehicleAsset{}, id, "vehicle asset")
}

// ListVehicleAssets retrieves all vehicle assets of the scope's household.
func (c *Client) ListVehicleAssets(s identity.Scope) ([]models.VehicleAsset, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var assets []models.VehicleAsset
	result := query.Model(&models.VehicleAsset{}).Order("name ASC").Find(&assets)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list vehicle assets: %w", result.Error)
	}
	return assets, nil
}

// UpdateVehicleMileage updates the current odometer reading of a vehicle asset of the scope's household.
func (c *Client) UpdateVehicleMileage(s identity.Scope, id uint, mileageKm int) (*models.VehicleAsset, error) {
	asset, err := c.GetVehicleAsset(s, id)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("vehicle asset %d not found", id)
	}

	asset.CurrentMileageKm = mileageKm
	if err := c.UpdateVehicleAsset(s, asset); err != nil {
		return nil, err
	}
	return asset, nil
}

// writeBackInHousehold writes every column of row (a pointer to a model whose household_id the
// caller has stamped) over the row with this ID in the scope's household, as Save did, but
// never touches a row of another household. Zero rows updated is "not found".
func writeBackInHousehold(database *gorm.DB, s identity.Scope, id uint, row interface{}, what string) error {
	if !s.Valid() {
		return identity.ErrNoScope
	}
	if id == 0 {
		return fmt.Errorf("%s has no id", what)
	}
	result := database.Model(row).Where("household_id = ?", s.HouseholdID()).Select("*").Omit("id", "created_at").Updates(row)
	if result.Error != nil {
		return fmt.Errorf("failed to update %s %d: %w", what, id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%s %d not found", what, id)
	}
	return nil
}

// removeFromHousehold removes the row of model with this ID in the scope's household. Zero rows
// deleted is "not found".
func removeFromHousehold(c *Client, s identity.Scope, model interface{}, id uint, what string) error {
	q, err := c.scoped(s)
	if err != nil {
		return err
	}
	result := q.Where("id = ?", id).Delete(model)
	if result.Error != nil {
		return fmt.Errorf("failed to delete %s %d: %w", what, id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%s %d not found", what, id)
	}
	return nil
}
