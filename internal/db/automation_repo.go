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
		&models.ExternalAPIConnector{},
		&models.AutomationRule{},
		&models.UserPreference{},
		&models.PersonalCRMContact{},
	)
}

// AutomationRepository defines CRUD operations for external API connectors,
// event-driven automation rules, user customization preferences, and personal CRM contacts.
type AutomationRepository interface {
	// ExternalAPIConnector CRUD
	CreateAPIConnector(connector *models.ExternalAPIConnector) error
	GetAPIConnector(id uint, familyGroup ...string) (*models.ExternalAPIConnector, error)
	GetAPIConnectorByName(userID, familyGroup, name string) (*models.ExternalAPIConnector, error)
	ListAPIConnectors(userID, familyGroup string) ([]models.ExternalAPIConnector, error)
	DeleteAPIConnector(id uint) error

	// AutomationRule CRUD
	CreateAutomationRule(rule *models.AutomationRule) error
	GetAutomationRule(id uint) (*models.AutomationRule, error)
	ListAutomationRules(userID, familyGroup, triggerTopic string) ([]models.AutomationRule, error)
	ListActiveAutomationRules(triggerTopic string) ([]models.AutomationRule, error)
	UpdateAutomationRule(rule *models.AutomationRule) error
	DeleteAutomationRule(id uint, familyGroup ...string) error

	// UserPreference CRUD
	UpsertUserPreference(pref *models.UserPreference) error
	GetUserPreference(userID string) (*models.UserPreference, error)

	// PersonalCRMContact CRUD
	UpsertCRMContact(contact *models.PersonalCRMContact) error
	GetCRMContact(id uint) (*models.PersonalCRMContact, error)
	ListCRMContacts(userID, familyGroup, query string) ([]models.PersonalCRMContact, error)
	DeleteCRMContact(id uint) error
}

// NewAutomationRepository initializes an AutomationRepository backed by a *gorm.DB instance.
func NewAutomationRepository(db *gorm.DB) AutomationRepository {
	return &Client{DB: db}
}

// CreateAPIConnector inserts a new ExternalAPIConnector into the database.
func (c *Client) CreateAPIConnector(connector *models.ExternalAPIConnector) error {
	if connector == nil {
		return fmt.Errorf("connector cannot be nil")
	}
	if connector.CreatedAt.IsZero() {
		connector.CreatedAt = time.Now()
	}
	if connector.DefaultMethod == "" {
		connector.DefaultMethod = "GET"
	}
	connector.Name = strings.TrimSpace(connector.Name)
	result := c.DB.Create(connector)
	if result.Error != nil {
		return fmt.Errorf("failed to create api connector: %w", result.Error)
	}
	return nil
}

// GetAPIConnector retrieves an ExternalAPIConnector by its primary key ID, optionally scoped to tenant.
// Returns (nil, nil) if not found.
func (c *Client) GetAPIConnector(id uint, familyGroup ...string) (*models.ExternalAPIConnector, error) {
	var conn models.ExternalAPIConnector
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.First(&conn)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get api connector: %w", result.Error)
	}
	return &conn, nil
}

// GetAPIConnectorByName retrieves an ExternalAPIConnector by name for a user or family group.
// Returns (nil, nil) if not found.
func (c *Client) GetAPIConnectorByName(userID, familyGroup, name string) (*models.ExternalAPIConnector, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("connector name cannot be empty")
	}

	var conn models.ExternalAPIConnector
	query := c.DB.Where("LOWER(name) = ?", strings.ToLower(name))
	if userID != "" && familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup, userID)
	} else if userID != "" {
		query = query.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		query = query.Where("family_group = ? AND family_group != ''", familyGroup)
	}

	result := query.First(&conn)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get api connector by name: %w", result.Error)
	}
	return &conn, nil
}

// ListAPIConnectors lists all ExternalAPIConnectors matching the given user or family group.
func (c *Client) ListAPIConnectors(userID, familyGroup string) ([]models.ExternalAPIConnector, error) {
	query := c.DB.Model(&models.ExternalAPIConnector{})
	if userID != "" && familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup, userID)
	} else if userID != "" {
		query = query.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		query = query.Where("family_group = ? AND family_group != ''", familyGroup)
	}

	var connectors []models.ExternalAPIConnector
	if err := query.Order("created_at DESC").Find(&connectors).Error; err != nil {
		return nil, fmt.Errorf("failed to list api connectors: %w", err)
	}
	return connectors, nil
}

// DeleteAPIConnector removes an ExternalAPIConnector by its primary key ID.
func (c *Client) DeleteAPIConnector(id uint) error {
	result := c.DB.Delete(&models.ExternalAPIConnector{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete api connector: %w", result.Error)
	}
	return nil
}

// CreateAutomationRule inserts a new AutomationRule into the database.
func (c *Client) CreateAutomationRule(rule *models.AutomationRule) error {
	if rule == nil {
		return fmt.Errorf("automation rule cannot be nil")
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}
	if rule.ConditionJSON == "" {
		rule.ConditionJSON = "{}"
	}
	rule.TriggerTopic = strings.ToLower(strings.TrimSpace(rule.TriggerTopic))
	rule.TriggerType = strings.ToLower(strings.TrimSpace(rule.TriggerType))

	result := c.DB.Create(rule)
	if result.Error != nil {
		return fmt.Errorf("failed to create automation rule: %w", result.Error)
	}
	return nil
}

// GetAutomationRule retrieves an AutomationRule by its primary key ID.
// Returns (nil, nil) if not found.
func (c *Client) GetAutomationRule(id uint) (*models.AutomationRule, error) {
	var rule models.AutomationRule
	result := c.DB.First(&rule, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get automation rule: %w", result.Error)
	}
	return &rule, nil
}

// ListAutomationRules lists AutomationRules filtered by user/family and optionally trigger topic.
func (c *Client) ListAutomationRules(userID, familyGroup, triggerTopic string) ([]models.AutomationRule, error) {
	query := c.DB.Model(&models.AutomationRule{})
	if userID != "" && familyGroup != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup, userID)
	} else if userID != "" {
		query = query.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		query = query.Where("family_group = ? AND family_group != ''", familyGroup)
	}

	normTopic := strings.ToLower(strings.TrimSpace(triggerTopic))
	if normTopic != "" && normTopic != "all" && normTopic != "*" {
		query = query.Where("LOWER(trigger_topic) = ?", normTopic)
	}

	var rules []models.AutomationRule
	if err := query.Order("created_at DESC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("failed to list automation rules: %w", err)
	}
	return rules, nil
}

// ListActiveAutomationRules retrieves all active AutomationRules matching a trigger topic or event_mutation type.
func (c *Client) ListActiveAutomationRules(triggerTopic string) ([]models.AutomationRule, error) {
	query := c.DB.Where("is_active = ?", true)
	normTopic := strings.ToLower(strings.TrimSpace(triggerTopic))
	if normTopic != "" && normTopic != "*" && normTopic != "all" {
		query = query.Where("(LOWER(trigger_topic) = ? OR trigger_topic = '*' OR trigger_topic = '' OR LOWER(trigger_type) = 'event_mutation')", normTopic)
	}

	var rules []models.AutomationRule
	if err := query.Order("id ASC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("failed to list active automation rules: %w", err)
	}
	return rules, nil
}

// UpdateAutomationRule updates an existing AutomationRule in the database.
func (c *Client) UpdateAutomationRule(rule *models.AutomationRule) error {
	if rule == nil {
		return fmt.Errorf("automation rule cannot be nil")
	}
	rule.TriggerTopic = strings.ToLower(strings.TrimSpace(rule.TriggerTopic))
	rule.TriggerType = strings.ToLower(strings.TrimSpace(rule.TriggerType))

	result := c.DB.Save(rule)
	if result.Error != nil {
		return fmt.Errorf("failed to update automation rule: %w", result.Error)
	}
	return nil
}

// DeleteAutomationRule removes an AutomationRule by its primary key ID, optionally scoped to tenant.
func (c *Client) DeleteAutomationRule(id uint, familyGroup ...string) error {
	query := c.DB.Where("id = ?", id)
	if len(familyGroup) > 0 && familyGroup[0] != "" {
		query = query.Where("((family_group = ? AND family_group != '') OR user_id = ?)", familyGroup[0], familyGroup[0])
	}
	result := query.Delete(&models.AutomationRule{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete automation rule: %w", result.Error)
	}
	return nil
}

// UpsertUserPreference creates or updates a UserPreference record for a given user.
func (c *Client) UpsertUserPreference(pref *models.UserPreference) error {
	if pref == nil {
		return fmt.Errorf("user preference cannot be nil")
	}
	if strings.TrimSpace(pref.UserID) == "" {
		return fmt.Errorf("user_id cannot be empty for user preference")
	}
	if pref.UpdatedAt.IsZero() {
		pref.UpdatedAt = time.Now()
	}
	if pref.BriefingTopicsJSON == "" {
		pref.BriefingTopicsJSON = "[]"
	}

	result := c.DB.Save(pref)
	if result.Error != nil {
		return fmt.Errorf("failed to upsert user preference: %w", result.Error)
	}
	return nil
}

// GetUserPreference retrieves the UserPreference record for a specific user ID.
// Returns (nil, nil) if not found.
func (c *Client) GetUserPreference(userID string) (*models.UserPreference, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("user_id cannot be empty")
	}
	var pref models.UserPreference
	result := c.DB.Where("user_id = ?", userID).First(&pref)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user preference: %w", result.Error)
	}
	return &pref, nil
}

// UpsertCRMContact creates or updates a PersonalCRMContact record.
func (c *Client) UpsertCRMContact(contact *models.PersonalCRMContact) error {
	if contact == nil {
		return fmt.Errorf("crm contact cannot be nil")
	}
	contact.PersonName = strings.TrimSpace(contact.PersonName)
	if contact.PersonName == "" {
		return fmt.Errorf("person_name cannot be empty")
	}
	if contact.ImportantDatesJSON == "" {
		contact.ImportantDatesJSON = "[]"
	}
	if contact.UpdatedAt.IsZero() {
		contact.UpdatedAt = time.Now()
	}

	result := c.DB.Save(contact)
	if result.Error != nil {
		return fmt.Errorf("failed to upsert crm contact: %w", result.Error)
	}
	return nil
}

// GetCRMContact retrieves a PersonalCRMContact by its primary key ID.
// Returns (nil, nil) if not found.
func (c *Client) GetCRMContact(id uint) (*models.PersonalCRMContact, error) {
	var contact models.PersonalCRMContact
	result := c.DB.First(&contact, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get crm contact: %w", result.Error)
	}
	return &contact, nil
}

// ListCRMContacts retrieves personal CRM contacts filtered by user/family and an optional search query.
func (c *Client) ListCRMContacts(userID, familyGroup, query string) ([]models.PersonalCRMContact, error) {
	dbQuery := c.DB.Model(&models.PersonalCRMContact{})
	if userID != "" && familyGroup != "" {
		dbQuery = dbQuery.Where("(user_id = ? OR family_group = ?)", userID, familyGroup)
	} else if userID != "" {
		dbQuery = dbQuery.Where("user_id = ?", userID)
	} else if familyGroup != "" {
		dbQuery = dbQuery.Where("family_group = ?", familyGroup)
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" {
		pattern := "%" + q + "%"
		dbQuery = dbQuery.Where("(LOWER(person_name) LIKE ? OR LOWER(relationship) LIKE ? OR LOWER(gift_ideas_and_sizes) LIKE ? OR LOWER(medical_notes) LIKE ?)", pattern, pattern, pattern, pattern)
	}

	var contacts []models.PersonalCRMContact
	if err := dbQuery.Order("person_name ASC").Find(&contacts).Error; err != nil {
		return nil, fmt.Errorf("failed to list crm contacts: %w", err)
	}
	return contacts, nil
}

// DeleteCRMContact removes a PersonalCRMContact by primary key ID.
func (c *Client) DeleteCRMContact(id uint) error {
	result := c.DB.Delete(&models.PersonalCRMContact{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete crm contact: %w", result.Error)
	}
	return nil
}
