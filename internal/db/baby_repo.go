package db

import (
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

func init() {
	RegisterModel(
		&models.BabyProfile{},
		&models.BabyCareLog{},
		&models.BabyInventoryItem{},
	)
}

// BabyRepository defines all persistence operations for the Baby & Family Care Tracker. Every
// method is confined to the scope's household (V4-30): reads filter by household_id, writes
// stamp it.
type BabyRepository interface {
	// Baby Profile CRUD
	UpsertBabyProfile(s identity.Scope, profile *models.BabyProfile) error
	GetBabyProfile(s identity.Scope, id uint) (*models.BabyProfile, error)
	GetBabyProfileByName(s identity.Scope, name string) (*models.BabyProfile, error)
	GetBabyProfilesForFamily(s identity.Scope) ([]models.BabyProfile, error)
	DeleteBabyProfile(s identity.Scope, id uint) error

	// Baby Care Log CRUD & Analytics
	CreateCareLog(s identity.Scope, log *models.BabyCareLog) error
	GetCareLogByID(s identity.Scope, id uint) (*models.BabyCareLog, error)
	GetCareLogs(s identity.Scope, babyID uint, logType string, start, end time.Time, limit int) ([]models.BabyCareLog, error)
	DeleteCareLog(s identity.Scope, id uint) error
	GetLastMedicationLog(s identity.Scope, babyID uint, medName string) (*models.BabyCareLog, error)
	GetCareStatsSummary(s identity.Scope, babyID uint, start, end time.Time) (*models.BabyCareSummary, error)

	// Baby Supplies & Inventory
	UpsertBabyInventoryItem(s identity.Scope, item *models.BabyInventoryItem) error
	GetBabyInventoryItem(s identity.Scope, id uint) (*models.BabyInventoryItem, error)
	GetBabyInventoryItemByName(s identity.Scope, itemName string) (*models.BabyInventoryItem, error)
	ListBabyInventoryItems(s identity.Scope) ([]models.BabyInventoryItem, error)
	GetLowBabyInventoryItems(s identity.Scope) ([]models.BabyInventoryItem, error)
	DeleteBabyInventoryItem(s identity.Scope, id uint) error
}

// NewBabyRepository constructs a BabyRepository backed by the given gorm.DB.
func NewBabyRepository(db *gorm.DB) BabyRepository {
	return &Client{DB: db}
}

// UpsertBabyProfile creates or updates a child profile in the scope's household. A profile ID
// that is not in the scope's household is "not found": it is never created under that ID.
func (c *Client) UpsertBabyProfile(s identity.Scope, profile *models.BabyProfile) error {
	if profile == nil {
		return fmt.Errorf("baby profile cannot be nil")
	}
	if strings.TrimSpace(profile.Name) == "" {
		return fmt.Errorf("baby name is required")
	}
	if err := stamp(s, &profile.HouseholdID, &profile.FamilyGroup); err != nil {
		return err
	}

	profile.Name = strings.TrimSpace(profile.Name)
	profile.UpdatedAt = time.Now()

	if profile.ID != 0 {
		var existing models.BabyProfile
		if err := c.DB.Where("household_id = ? AND id = ?", s.HouseholdID(), profile.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("baby profile %d not found", profile.ID)
			}
			return fmt.Errorf("failed to lookup baby profile: %w", err)
		}
		return c.DB.Model(&existing).Where("household_id = ?", s.HouseholdID()).Updates(profile).Error
	}

	// Match on household + Name
	var existing models.BabyProfile
	err := c.DB.Where("household_id = ? AND LOWER(name) = ?", s.HouseholdID(), strings.ToLower(profile.Name)).First(&existing).Error
	if err == nil {
		profile.ID = existing.ID
		return c.DB.Model(&existing).Where("household_id = ?", s.HouseholdID()).Updates(profile).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.DB.Create(profile).Error
	}
	return fmt.Errorf("failed to query existing profile: %w", err)
}

// GetBabyProfile fetches a baby profile of the scope's household by primary key.
func (c *Client) GetBabyProfile(s identity.Scope, id uint) (*models.BabyProfile, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var profile models.BabyProfile
	err = q.Where("id = ?", id).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby profile: %w", err)
	}
	return &profile, nil
}

// GetBabyProfileByName finds a profile of the scope's household by name (case-insensitive).
func (c *Client) GetBabyProfileByName(s identity.Scope, name string) (*models.BabyProfile, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var profile models.BabyProfile
	err = q.Where("LOWER(name) = ?", strings.ToLower(strings.TrimSpace(name))).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby profile by name: %w", err)
	}
	return &profile, nil
}

// GetBabyProfilesForFamily returns all baby profiles registered to the scope's household.
func (c *Client) GetBabyProfilesForFamily(s identity.Scope) ([]models.BabyProfile, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var profiles []models.BabyProfile
	err = q.Order("date_of_birth ASC").Find(&profiles).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list baby profiles for family: %w", err)
	}
	return profiles, nil
}

// DeleteBabyProfile removes a baby profile of the scope's household.
func (c *Client) DeleteBabyProfile(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.BabyProfile{}, id, "baby profile")
}

// CreateCareLog inserts a new care log entry into the scope's household.
func (c *Client) CreateCareLog(s identity.Scope, log *models.BabyCareLog) error {
	if log == nil {
		return fmt.Errorf("care log cannot be nil")
	}
	if err := stamp(s, &log.HouseholdID, &log.FamilyGroup); err != nil {
		return err
	}
	if strings.TrimSpace(log.LogType) == "" {
		return fmt.Errorf("log type is required")
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now()
	}
	log.LogType = strings.ToLower(strings.TrimSpace(log.LogType))

	if err := c.DB.Create(log).Error; err != nil {
		return fmt.Errorf("failed to insert baby care log: %w", err)
	}
	return nil
}

// GetCareLogByID retrieves a single care log of the scope's household by its ID.
func (c *Client) GetCareLogByID(s identity.Scope, id uint) (*models.BabyCareLog, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var log models.BabyCareLog
	err = q.Where("id = ?", id).First(&log).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get care log by id: %w", err)
	}
	return &log, nil
}

// GetCareLogs queries the scope household's care logs using multiple optional filters.
func (c *Client) GetCareLogs(s identity.Scope, babyID uint, logType string, start, end time.Time, limit int) ([]models.BabyCareLog, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = query.Model(&models.BabyCareLog{})

	if babyID > 0 {
		query = query.Where("baby_id = ?", babyID)
	}
	if strings.TrimSpace(logType) != "" && !strings.EqualFold(logType, "all") {
		query = query.Where("LOWER(log_type) = ?", strings.ToLower(strings.TrimSpace(logType)))
	}
	if !start.IsZero() {
		query = query.Where("timestamp >= ?", start)
	}
	if !end.IsZero() {
		query = query.Where("timestamp <= ?", end)
	}

	query = query.Order("timestamp DESC")

	if limit > 0 {
		query = query.Limit(limit)
	} else {
		query = query.Limit(100)
	}

	var logs []models.BabyCareLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to list care logs: %w", err)
	}
	return logs, nil
}

// DeleteCareLog deletes a single care log entry of the scope's household.
func (c *Client) DeleteCareLog(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.BabyCareLog{}, id, "care log")
}

// GetLastMedicationLog retrieves the most recent medication log entry for a baby of the scope's household.
func (c *Client) GetLastMedicationLog(s identity.Scope, babyID uint, medName string) (*models.BabyCareLog, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = query.Model(&models.BabyCareLog{}).Where("LOWER(log_type) = ?", "medication")

	if babyID > 0 {
		query = query.Where("baby_id = ?", babyID)
	}
	if strings.TrimSpace(medName) != "" {
		norm := strings.ToLower(strings.TrimSpace(medName))
		if strings.Contains(norm, "paracetamol") || strings.Contains(norm, "acetaminophen") ||
			strings.Contains(norm, "tylenol") || strings.Contains(norm, "calpol") ||
			strings.Contains(norm, "acamol") || strings.Contains(norm, "panadol") ||
			strings.Contains(norm, "febricol") {
			query = query.Where("LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ?",
				"%paracetamol%", "%acetaminophen%", "%tylenol%", "%calpol%", "%acamol%", "%panadol%", "%febricol%")
		} else if strings.Contains(norm, "ibuprofen") || strings.Contains(norm, "advil") ||
			strings.Contains(norm, "nurofen") || strings.Contains(norm, "motrin") ||
			strings.Contains(norm, "pediaprofen") {
			query = query.Where("LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ? OR LOWER(details) LIKE ?",
				"%ibuprofen%", "%advil%", "%nurofen%", "%motrin%", "%pediaprofen%")
		} else {
			query = query.Where("LOWER(details) LIKE ?", "%"+norm+"%")
		}
	}

	var log models.BabyCareLog
	err = query.Order("timestamp DESC").First(&log).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get last medication log: %w", err)
	}
	return &log, nil
}

// GetCareStatsSummary computes aggregated metrics over a date range for the scope's household.
func (c *Client) GetCareStatsSummary(s identity.Scope, babyID uint, start, end time.Time) (*models.BabyCareSummary, error) {
	logs, err := c.GetCareLogs(s, babyID, "all", start, end, 1000)
	if err != nil {
		return nil, err
	}

	summary := &models.BabyCareSummary{
		BabyID:         babyID,
		FamilyGroup:    s.LegacyLabel(),
		StartTime:      start,
		EndTime:        end,
		LogCountByType: make(map[string]int),
	}

	for i := range logs {
		l := logs[i]
		summary.LogCountByType[l.LogType]++

		switch l.LogType {
		case "feeding":
			summary.TotalFeedings++
			summary.TotalFeedingVolume += l.ValueNumeric
			if summary.FeedingVolumeUnit == "" && l.Unit != "" {
				summary.FeedingVolumeUnit = l.Unit
			}
		case "sleep":
			// Normalize sleep value to minutes
			if strings.EqualFold(l.Unit, "hours") || strings.EqualFold(l.Unit, "hr") || strings.EqualFold(l.Unit, "h") {
				summary.TotalSleepMinutes += l.ValueNumeric * 60
			} else {
				summary.TotalSleepMinutes += l.ValueNumeric
			}
		case "diaper":
			summary.TotalDiaperChanges++
			detailLower := strings.ToLower(l.Details)
			if strings.Contains(detailLower, "both") || (strings.Contains(detailLower, "wet") && strings.Contains(detailLower, "dirty")) {
				summary.BothDiapers++
			} else if strings.Contains(detailLower, "dirty") || strings.Contains(detailLower, "bm") || strings.Contains(detailLower, "poop") {
				summary.DirtyDiapers++
			} else {
				summary.WetDiapers++
			}
		case "temperature":
			if summary.LastTemperature == nil {
				val := l.ValueNumeric
				summary.LastTemperature = &val
				summary.LastTempUnit = l.Unit
			}
		case "medication":
			if summary.LastMedication == nil {
				cp := l
				summary.LastMedication = &cp
			}
		}
	}

	return summary, nil
}

// UpsertBabyInventoryItem creates or updates a tracked baby inventory item in the scope's
// household. An item ID that is not in the scope's household is "not found".
func (c *Client) UpsertBabyInventoryItem(s identity.Scope, item *models.BabyInventoryItem) error {
	if item == nil {
		return fmt.Errorf("inventory item cannot be nil")
	}
	if strings.TrimSpace(item.ItemName) == "" {
		return fmt.Errorf("item name is required")
	}
	if err := stamp(s, &item.HouseholdID, &item.FamilyGroup); err != nil {
		return err
	}

	item.ItemName = strings.TrimSpace(item.ItemName)
	item.UpdatedAt = time.Now()

	if item.ID != 0 {
		var existing models.BabyInventoryItem
		if err := c.DB.Where("household_id = ? AND id = ?", s.HouseholdID(), item.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("inventory item %d not found", item.ID)
			}
			return fmt.Errorf("failed to lookup inventory item: %w", err)
		}
		return c.DB.Model(&existing).Where("household_id = ?", s.HouseholdID()).Updates(item).Error
	}

	// Match on household + ItemName
	var existing models.BabyInventoryItem
	err := c.DB.Where("household_id = ? AND LOWER(item_name) = ?", s.HouseholdID(), strings.ToLower(item.ItemName)).First(&existing).Error
	if err == nil {
		item.ID = existing.ID
		return c.DB.Model(&existing).Where("household_id = ?", s.HouseholdID()).Updates(map[string]interface{}{
			"quantity_remaining": item.QuantityRemaining,
			"unit":               item.Unit,
			"low_threshold":      item.LowThreshold,
			"updated_at":         item.UpdatedAt,
		}).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.DB.Create(item).Error
	}
	return fmt.Errorf("failed to query inventory item: %w", err)
}

// GetBabyInventoryItem fetches a baby inventory item of the scope's household by ID.
func (c *Client) GetBabyInventoryItem(s identity.Scope, id uint) (*models.BabyInventoryItem, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var item models.BabyInventoryItem
	err = q.Where("id = ?", id).First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby inventory item: %w", err)
	}
	return &item, nil
}

// GetBabyInventoryItemByName finds an inventory item of the scope's household by name.
func (c *Client) GetBabyInventoryItemByName(s identity.Scope, itemName string) (*models.BabyInventoryItem, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var item models.BabyInventoryItem
	err = q.Where("LOWER(item_name) = ?", strings.ToLower(strings.TrimSpace(itemName))).First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get inventory item by name: %w", err)
	}
	return &item, nil
}

// ListBabyInventoryItems returns all inventory items of the scope's household.
func (c *Client) ListBabyInventoryItems(s identity.Scope) ([]models.BabyInventoryItem, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var items []models.BabyInventoryItem
	err = q.Order("item_name ASC").Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list inventory items: %w", err)
	}
	return items, nil
}

// GetLowBabyInventoryItems finds the scope household's items where QuantityRemaining <= LowThreshold.
func (c *Client) GetLowBabyInventoryItems(s identity.Scope) ([]models.BabyInventoryItem, error) {
	q, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var items []models.BabyInventoryItem
	err = q.Where("quantity_remaining <= low_threshold").Order("quantity_remaining ASC").Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query low inventory items: %w", err)
	}
	return items, nil
}

// DeleteBabyInventoryItem removes an inventory item of the scope's household by primary key.
func (c *Client) DeleteBabyInventoryItem(s identity.Scope, id uint) error {
	return removeFromHousehold(c, s, &models.BabyInventoryItem{}, id, "inventory item")
}
