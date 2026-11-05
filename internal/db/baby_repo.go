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
		&models.BabyProfile{},
		&models.BabyCareLog{},
		&models.BabyInventoryItem{},
	)
}

// BabyRepository defines all persistence operations for the Baby & Family Care Tracker.
type BabyRepository interface {
	// Baby Profile CRUD
	UpsertBabyProfile(profile *models.BabyProfile) error
	GetBabyProfile(id uint) (*models.BabyProfile, error)
	GetBabyProfileByName(familyGroup, name string) (*models.BabyProfile, error)
	GetBabyProfilesForFamily(familyGroup string) ([]models.BabyProfile, error)
	DeleteBabyProfile(id uint) error

	// Baby Care Log CRUD & Analytics
	CreateCareLog(log *models.BabyCareLog) error
	GetCareLogByID(id uint) (*models.BabyCareLog, error)
	GetCareLogs(babyID uint, familyGroup, logType string, start, end time.Time, limit int) ([]models.BabyCareLog, error)
	DeleteCareLog(id uint) error
	GetLastMedicationLog(babyID uint, familyGroup, medName string) (*models.BabyCareLog, error)
	GetCareStatsSummary(babyID uint, familyGroup string, start, end time.Time) (*models.BabyCareSummary, error)

	// Baby Supplies & Inventory
	UpsertBabyInventoryItem(item *models.BabyInventoryItem) error
	GetBabyInventoryItem(id uint) (*models.BabyInventoryItem, error)
	GetBabyInventoryItemByName(familyGroup, itemName string) (*models.BabyInventoryItem, error)
	ListBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error)
	GetLowBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error)
	DeleteBabyInventoryItem(id uint) error
}

// NewBabyRepository constructs a BabyRepository backed by the given gorm.DB.
func NewBabyRepository(db *gorm.DB) BabyRepository {
	return &Client{DB: db}
}

// UpsertBabyProfile creates or updates a child profile.
func (c *Client) UpsertBabyProfile(profile *models.BabyProfile) error {
	if profile == nil {
		return fmt.Errorf("baby profile cannot be nil")
	}
	if strings.TrimSpace(profile.Name) == "" {
		return fmt.Errorf("baby name is required")
	}
	if strings.TrimSpace(profile.FamilyGroup) == "" {
		return fmt.Errorf("family group is required")
	}

	profile.Name = strings.TrimSpace(profile.Name)
	profile.FamilyGroup = strings.TrimSpace(profile.FamilyGroup)
	profile.UpdatedAt = time.Now()

	if profile.ID != 0 {
		var existing models.BabyProfile
		if err := c.DB.First(&existing, profile.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.DB.Create(profile).Error
			}
			return fmt.Errorf("failed to lookup baby profile: %w", err)
		}
		return c.DB.Model(&existing).Updates(profile).Error
	}

	// Match on FamilyGroup + Name
	var existing models.BabyProfile
	err := c.DB.Where("family_group = ? AND LOWER(name) = ?", profile.FamilyGroup, strings.ToLower(profile.Name)).First(&existing).Error
	if err == nil {
		profile.ID = existing.ID
		return c.DB.Model(&existing).Updates(profile).Error
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.DB.Create(profile).Error
	}
	return fmt.Errorf("failed to query existing profile: %w", err)
}

// GetBabyProfile fetches a baby profile by primary key.
func (c *Client) GetBabyProfile(id uint) (*models.BabyProfile, error) {
	var profile models.BabyProfile
	err := c.DB.First(&profile, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby profile: %w", err)
	}
	return &profile, nil
}

// GetBabyProfileByName finds a profile by family group and name (case-insensitive).
func (c *Client) GetBabyProfileByName(familyGroup, name string) (*models.BabyProfile, error) {
	var profile models.BabyProfile
	err := c.DB.Where("family_group = ? AND LOWER(name) = ?", strings.TrimSpace(familyGroup), strings.ToLower(strings.TrimSpace(name))).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby profile by name: %w", err)
	}
	return &profile, nil
}

// GetBabyProfilesForFamily returns all baby profiles registered to a family group.
func (c *Client) GetBabyProfilesForFamily(familyGroup string) ([]models.BabyProfile, error) {
	var profiles []models.BabyProfile
	err := c.DB.Where("family_group = ?", strings.TrimSpace(familyGroup)).Order("date_of_birth ASC").Find(&profiles).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list baby profiles for family: %w", err)
	}
	return profiles, nil
}

// DeleteBabyProfile removes a baby profile and associated logs.
func (c *Client) DeleteBabyProfile(id uint) error {
	result := c.DB.Delete(&models.BabyProfile{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete baby profile: %w", result.Error)
	}
	return nil
}

// CreateCareLog inserts a new care log entry.
func (c *Client) CreateCareLog(log *models.BabyCareLog) error {
	if log == nil {
		return fmt.Errorf("care log cannot be nil")
	}
	if strings.TrimSpace(log.LogType) == "" {
		return fmt.Errorf("log type is required")
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now()
	}
	log.LogType = strings.ToLower(strings.TrimSpace(log.LogType))
	log.FamilyGroup = strings.TrimSpace(log.FamilyGroup)

	if err := c.DB.Create(log).Error; err != nil {
		return fmt.Errorf("failed to insert baby care log: %w", err)
	}
	return nil
}

// GetCareLogByID retrieves a single care log by its ID.
func (c *Client) GetCareLogByID(id uint) (*models.BabyCareLog, error) {
	var log models.BabyCareLog
	err := c.DB.First(&log, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get care log by id: %w", err)
	}
	return &log, nil
}

// GetCareLogs queries care logs using multiple optional filters.
func (c *Client) GetCareLogs(babyID uint, familyGroup, logType string, start, end time.Time, limit int) ([]models.BabyCareLog, error) {
	query := c.DB.Model(&models.BabyCareLog{})

	if babyID > 0 {
		query = query.Where("baby_id = ?", babyID)
	}
	if strings.TrimSpace(familyGroup) != "" {
		query = query.Where("family_group = ?", strings.TrimSpace(familyGroup))
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

// DeleteCareLog deletes a single care log entry.
func (c *Client) DeleteCareLog(id uint) error {
	result := c.DB.Delete(&models.BabyCareLog{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete care log: %w", result.Error)
	}
	return nil
}

// GetLastMedicationLog retrieves the most recent medication log entry for a baby/family.
func (c *Client) GetLastMedicationLog(babyID uint, familyGroup, medName string) (*models.BabyCareLog, error) {
	query := c.DB.Model(&models.BabyCareLog{}).Where("LOWER(log_type) = ?", "medication")

	if babyID > 0 {
		query = query.Where("baby_id = ?", babyID)
	}
	if strings.TrimSpace(familyGroup) != "" {
		query = query.Where("family_group = ?", strings.TrimSpace(familyGroup))
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
	err := query.Order("timestamp DESC").First(&log).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get last medication log: %w", err)
	}
	return &log, nil
}

// GetCareStatsSummary computes aggregated metrics over a date range.
func (c *Client) GetCareStatsSummary(babyID uint, familyGroup string, start, end time.Time) (*models.BabyCareSummary, error) {
	logs, err := c.GetCareLogs(babyID, familyGroup, "all", start, end, 1000)
	if err != nil {
		return nil, err
	}

	summary := &models.BabyCareSummary{
		BabyID:         babyID,
		FamilyGroup:    familyGroup,
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

// UpsertBabyInventoryItem creates or updates a tracked baby inventory item.
func (c *Client) UpsertBabyInventoryItem(item *models.BabyInventoryItem) error {
	if item == nil {
		return fmt.Errorf("inventory item cannot be nil")
	}
	if strings.TrimSpace(item.ItemName) == "" {
		return fmt.Errorf("item name is required")
	}
	if strings.TrimSpace(item.FamilyGroup) == "" {
		return fmt.Errorf("family group is required")
	}

	item.ItemName = strings.TrimSpace(item.ItemName)
	item.FamilyGroup = strings.TrimSpace(item.FamilyGroup)
	item.UpdatedAt = time.Now()

	if item.ID != 0 {
		var existing models.BabyInventoryItem
		if err := c.DB.First(&existing, item.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.DB.Create(item).Error
			}
			return fmt.Errorf("failed to lookup inventory item: %w", err)
		}
		return c.DB.Model(&existing).Updates(item).Error
	}

	// Match on FamilyGroup + ItemName
	var existing models.BabyInventoryItem
	err := c.DB.Where("family_group = ? AND LOWER(item_name) = ?", item.FamilyGroup, strings.ToLower(item.ItemName)).First(&existing).Error
	if err == nil {
		item.ID = existing.ID
		return c.DB.Model(&existing).Updates(map[string]interface{}{
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

// GetBabyInventoryItem fetches a baby inventory item by ID.
func (c *Client) GetBabyInventoryItem(id uint) (*models.BabyInventoryItem, error) {
	var item models.BabyInventoryItem
	err := c.DB.First(&item, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get baby inventory item: %w", err)
	}
	return &item, nil
}

// GetBabyInventoryItemByName finds an inventory item by family group and name.
func (c *Client) GetBabyInventoryItemByName(familyGroup, itemName string) (*models.BabyInventoryItem, error) {
	var item models.BabyInventoryItem
	err := c.DB.Where("family_group = ? AND LOWER(item_name) = ?", strings.TrimSpace(familyGroup), strings.ToLower(strings.TrimSpace(itemName))).First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get inventory item by name: %w", err)
	}
	return &item, nil
}

// ListBabyInventoryItems returns all inventory items for a family group.
func (c *Client) ListBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error) {
	var items []models.BabyInventoryItem
	err := c.DB.Where("family_group = ?", strings.TrimSpace(familyGroup)).Order("item_name ASC").Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list inventory items: %w", err)
	}
	return items, nil
}

// GetLowBabyInventoryItems finds items where QuantityRemaining <= LowThreshold.
func (c *Client) GetLowBabyInventoryItems(familyGroup string) ([]models.BabyInventoryItem, error) {
	var items []models.BabyInventoryItem
	err := c.DB.Where("family_group = ? AND quantity_remaining <= low_threshold", strings.TrimSpace(familyGroup)).Order("quantity_remaining ASC").Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query low inventory items: %w", err)
	}
	return items, nil
}

// DeleteBabyInventoryItem removes an inventory item by primary key.
func (c *Client) DeleteBabyInventoryItem(id uint) error {
	result := c.DB.Delete(&models.BabyInventoryItem{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete inventory item: %w", result.Error)
	}
	return nil
}
