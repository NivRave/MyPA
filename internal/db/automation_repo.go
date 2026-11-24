package db

import (
	"context"
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
		&models.ExternalAPIConnector{},
		&models.AutomationRule{},
		&models.UserPreference{},
		&models.PersonalCRMContact{},
	)
}

// AutomationRepository defines CRUD operations for external API connectors,
// event-driven automation rules, user customization preferences, and personal CRM contacts.
type AutomationRepository interface {
	// ExternalAPIConnector CRUD. Every method is confined to the scope's household (V4-30).
	CreateAPIConnector(s identity.Scope, connector *models.ExternalAPIConnector) error
	GetAPIConnector(s identity.Scope, id uint) (*models.ExternalAPIConnector, error)
	GetAPIConnectorByName(s identity.Scope, name string) (*models.ExternalAPIConnector, error)
	ListAPIConnectors(s identity.Scope) ([]models.ExternalAPIConnector, error)
	DeleteAPIConnector(s identity.Scope, id uint) error

	// AutomationRule CRUD
	CreateAutomationRule(s identity.Scope, rule *models.AutomationRule) error
	GetAutomationRule(s identity.Scope, id uint) (*models.AutomationRule, error)
	ListAutomationRules(s identity.Scope, triggerTopic string) ([]models.AutomationRule, error)
	// ListActiveAutomationRules scans every household (scheduled job and event runner): each
	// returned rule carries its HouseholdID, and callers act only within that household.
	ListActiveAutomationRules(triggerTopic string) ([]models.AutomationRule, error)
	UpdateAutomationRule(s identity.Scope, rule *models.AutomationRule) error
	DeleteAutomationRule(s identity.Scope, id uint) error

	// The automation repository also serves preferences and the CRM; the tables are unchanged.
	PreferencesRepository
}

// PreferencesRepository is the storage of the Core preferences module (V4-27): a person's
// preferences and the household's personal CRM.
type PreferencesRepository interface {
	// UserPreference CRUD (person-keyed, no household column)
	UpsertUserPreference(pref *models.UserPreference) error
	GetUserPreference(userID string) (*models.UserPreference, error)

	// PersonalCRMContact CRUD
	UpsertCRMContact(s identity.Scope, contact *models.PersonalCRMContact) error
	GetCRMContact(s identity.Scope, id uint) (*models.PersonalCRMContact, error)
	ListCRMContacts(s identity.Scope, query string) ([]models.PersonalCRMContact, error)
	DeleteCRMContact(s identity.Scope, id uint) error
}

// NewAutomationRepository initializes an AutomationRepository backed by a *gorm.DB instance.
func NewAutomationRepository(db *gorm.DB) AutomationRepository {
	return &Client{DB: db}
}

// CreateAPIConnector inserts a new ExternalAPIConnector into the scope's household.
func (c *Client) CreateAPIConnector(s identity.Scope, connector *models.ExternalAPIConnector) error {
	if connector == nil {
		return fmt.Errorf("connector cannot be nil")
	}
	if err := stamp(s, &connector.HouseholdID, &connector.FamilyGroup); err != nil {
		return err
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

// GetAPIConnector retrieves an ExternalAPIConnector of the scope's household by ID.
// Returns (nil, nil) if not found.
func (c *Client) GetAPIConnector(s identity.Scope, id uint) (*models.ExternalAPIConnector, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var conn models.ExternalAPIConnector
	result := query.Where("id = ?", id).First(&conn)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get api connector: %w", result.Error)
	}
	return &conn, nil
}

// GetAPIConnectorByName retrieves an ExternalAPIConnector of the scope's household by name.
// Returns (nil, nil) if not found.
func (c *Client) GetAPIConnectorByName(s identity.Scope, name string) (*models.ExternalAPIConnector, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("connector name cannot be empty")
	}
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}

	var conn models.ExternalAPIConnector
	result := query.Where("LOWER(name) = ?", strings.ToLower(name)).Order("id ASC").First(&conn)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get api connector by name: %w", result.Error)
	}
	return &conn, nil
}

// ListAPIConnectors lists the ExternalAPIConnectors of the scope's household.
func (c *Client) ListAPIConnectors(s identity.Scope) ([]models.ExternalAPIConnector, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}

	var connectors []models.ExternalAPIConnector
	if err := query.Order("created_at DESC").Find(&connectors).Error; err != nil {
		return nil, fmt.Errorf("failed to list api connectors: %w", err)
	}
	return connectors, nil
}

// DeleteAPIConnector removes an ExternalAPIConnector of the scope's household by ID.
func (c *Client) DeleteAPIConnector(s identity.Scope, id uint) error {
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	result := query.Where("id = ?", id).Delete(&models.ExternalAPIConnector{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete api connector: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("api connector %d not found", id)
	}
	return nil
}

// CreateAutomationRule inserts a new AutomationRule into the scope's household.
func (c *Client) CreateAutomationRule(s identity.Scope, rule *models.AutomationRule) error {
	if rule == nil {
		return fmt.Errorf("automation rule cannot be nil")
	}
	if err := stamp(s, &rule.HouseholdID, &rule.FamilyGroup); err != nil {
		return err
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

// workflowsOfOthersHidden confines a scoped rule query so that a scheduled workflow (a cron rule
// whose action is a prompt, V4-07) is seen, changed and deleted by its own person only. A
// workflow was one person's own before migration 11 folded it into the household's rules, and it
// stays so on every path: the rule tools, the dashboard and the toggle. Other rules are the
// household's, as before. The test matches automation.PromptOf.
func workflowsOfOthersHidden(q *gorm.DB, s identity.Scope) *gorm.DB {
	const workflow = `(trigger_type ~* '^\s*cron\s*$' AND action_instruction ~* '^\s*prompt:')`
	key := strings.TrimSpace(s.PersonKey())
	if key == "" {
		return q.Where("NOT " + workflow)
	}
	mine, args := personIs("user_id", "person_id", key)
	return q.Where("(NOT "+workflow+" OR "+mine+")", args...)
}

// GetAutomationRule retrieves an AutomationRule of the scope's household by ID.
// Returns (nil, nil) if not found, or if it is another person's workflow.
func (c *Client) GetAutomationRule(s identity.Scope, id uint) (*models.AutomationRule, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = workflowsOfOthersHidden(query, s)
	var rule models.AutomationRule
	result := query.Where("id = ?", id).First(&rule)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get automation rule: %w", result.Error)
	}
	return &rule, nil
}

// ListAutomationRules lists the scope household's AutomationRules, optionally by trigger topic,
// without other people's workflows.
func (c *Client) ListAutomationRules(s identity.Scope, triggerTopic string) ([]models.AutomationRule, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	query = workflowsOfOthersHidden(query.Model(&models.AutomationRule{}), s)

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

// ListActiveAutomationRules retrieves all active AutomationRules matching a trigger topic or
// event_mutation type. It scans every household (scheduled job and event runner): each rule
// carries its HouseholdID, and callers act only within that household (V4-30).
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

// UpdateAutomationRule updates an existing AutomationRule of the scope's household. Another
// person's workflow is not found.
func (c *Client) UpdateAutomationRule(s identity.Scope, rule *models.AutomationRule) error {
	if rule == nil || rule.ID == 0 {
		return fmt.Errorf("automation rule with an ID is required")
	}
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	query = workflowsOfOthersHidden(query, s)
	if err := stamp(s, &rule.HouseholdID, &rule.FamilyGroup); err != nil {
		return err
	}
	rule.TriggerTopic = strings.ToLower(strings.TrimSpace(rule.TriggerTopic))
	rule.TriggerType = strings.ToLower(strings.TrimSpace(rule.TriggerType))
	if rule.ConditionJSON == "" {
		rule.ConditionJSON = "{}"
	}

	result := query.Model(&models.AutomationRule{}).Where("id = ?", rule.ID).Updates(map[string]interface{}{
		"user_id":            rule.UserID,
		"family_group":       rule.FamilyGroup,
		"name":               rule.Name,
		"trigger_type":       rule.TriggerType,
		"trigger_topic":      rule.TriggerTopic,
		"condition_json":     rule.ConditionJSON,
		"action_instruction": rule.ActionInstruction,
		"is_active":          rule.IsActive,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update automation rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("automation rule %d not found", rule.ID)
	}
	return nil
}

// DeleteAutomationRule removes an AutomationRule of the scope's household by ID. A rule of
// another household, or another person's workflow, is left untouched (no error, nothing deleted).
func (c *Client) DeleteAutomationRule(s identity.Scope, id uint) error {
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	query = workflowsOfOthersHidden(query, s)
	result := query.Where("id = ?", id).Delete(&models.AutomationRule{})
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
	where, args := personIs("user_id", "person_id", userID)
	result := c.DB.Where(where, args...).Order("person_id IS NULL").First(&pref)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user preference: %w", result.Error)
	}
	return &pref, nil
}

// UpsertCRMContact creates or updates a PersonalCRMContact of the scope's household. An ID that
// is not in the household is refused, never created.
func (c *Client) UpsertCRMContact(s identity.Scope, contact *models.PersonalCRMContact) error {
	if contact == nil {
		return fmt.Errorf("crm contact cannot be nil")
	}
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	if err := stamp(s, &contact.HouseholdID, &contact.FamilyGroup); err != nil {
		return err
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

	if contact.ID == 0 {
		if result := c.DB.Create(contact); result.Error != nil {
			return fmt.Errorf("failed to upsert crm contact: %w", result.Error)
		}
		return nil
	}
	result := query.Model(&models.PersonalCRMContact{}).Where("id = ?", contact.ID).Updates(map[string]interface{}{
		"user_id":              contact.UserID,
		"family_group":         contact.FamilyGroup,
		"person_name":          contact.PersonName,
		"relationship":         contact.Relationship,
		"important_dates_json": contact.ImportantDatesJSON,
		"gift_ideas_and_sizes": contact.GiftIdeasAndSizes,
		"medical_notes":        contact.MedicalNotes,
		"updated_at":           contact.UpdatedAt,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to upsert crm contact: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("crm contact %d not found", contact.ID)
	}
	return nil
}

// GetCRMContact retrieves a PersonalCRMContact of the scope's household by ID.
// Returns (nil, nil) if not found.
func (c *Client) GetCRMContact(s identity.Scope, id uint) (*models.PersonalCRMContact, error) {
	query, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	var contact models.PersonalCRMContact
	result := query.Where("id = ?", id).First(&contact)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get crm contact: %w", result.Error)
	}
	return &contact, nil
}

// ListCRMContacts retrieves the scope household's personal CRM contacts, optionally by a search query.
func (c *Client) ListCRMContacts(s identity.Scope, query string) ([]models.PersonalCRMContact, error) {
	dbQuery, err := c.scoped(s)
	if err != nil {
		return nil, err
	}
	dbQuery = dbQuery.Model(&models.PersonalCRMContact{})

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

// DeleteCRMContact removes a PersonalCRMContact of the scope's household by ID.
func (c *Client) DeleteCRMContact(s identity.Scope, id uint) error {
	query, err := c.scoped(s)
	if err != nil {
		return err
	}
	result := query.Where("id = ?", id).Delete(&models.PersonalCRMContact{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete crm contact: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("crm contact %d not found", id)
	}
	return nil
}

// IdleRuleCount counts the household's automation rules whose action does nothing when they fire
// (BL-63, for /doctor): not a webhook, not a notify: message, and not a prompt: on a cron rule. A
// plain instruction was such an action before BL-63; existing rules are not rewritten. The tests
// match the automation package's (isWebhookAction, isNotifyAction, PromptOf).
func (c *Client) IdleRuleCount(ctx context.Context, s identity.Scope) (int64, error) {
	query, err := c.scoped(s)
	if err != nil {
		return 0, err
	}
	var n int64
	err = query.WithContext(ctx).Model(&models.AutomationRule{}).
		Where(`NOT (action_instruction ~ '^\s*https?://'
			OR action_instruction ~* '^\s*(webhook|notify):'
			OR (trigger_type ~* '^\s*cron\s*$' AND action_instruction ~* '^\s*prompt:'))`).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count idle automation rules: %w", err)
	}
	return n, nil
}
