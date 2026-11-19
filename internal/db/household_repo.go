package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Households (V4-30, ADR 0002). Every household-scoped repository method takes an
// identity.Scope and filters by its household_id; writes stamp household_id and, until V4-44,
// the legacy family_group label beside it.

var _ identity.Store = (*Client)(nil)

// HouseholdByID implements identity.Store.
func (c *Client) HouseholdByID(ctx context.Context, id string) (*models.Household, error) {
	var h models.Household
	err := c.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get household: %w", err)
	}
	return &h, nil
}

// HouseholdsByLabel implements identity.Store: legacy labels equal to label, trimmed, ignoring case.
func (c *Client) HouseholdsByLabel(ctx context.Context, label string) ([]models.Household, error) {
	var hs []models.Household
	err := c.DB.WithContext(ctx).
		Where("legacy_label IS NOT NULL AND lower(btrim(legacy_label)) = lower(?)", strings.TrimSpace(label)).
		Order("created_at, id").Find(&hs).Error
	if err != nil {
		return nil, fmt.Errorf("find households by label: %w", err)
	}
	return hs, nil
}

// CountHouseholds returns how many households exist, the holding household excluded.
func (c *Client) CountHouseholds(ctx context.Context) (int64, error) {
	var n int64
	err := c.DB.WithContext(ctx).Model(&models.Household{}).Where("id <> ?", models.HoldingHouseholdID).Count(&n).Error
	return n, err
}

// CreateHousehold inserts a household. A non-empty legacy label must be unique.
func (c *Client) CreateHousehold(ctx context.Context, h *models.Household) error {
	if h == nil || strings.TrimSpace(h.Name) == "" {
		return fmt.Errorf("a household needs a name")
	}
	if h.LegacyLabel != nil {
		l := strings.TrimSpace(*h.LegacyLabel)
		if l == "" {
			return fmt.Errorf("a household label cannot be empty")
		}
		h.LegacyLabel = &l
	}
	return c.DB.WithContext(ctx).Create(h).Error
}

// HouseholdSettings are the family-level settings an admin may change.
type HouseholdSettings struct {
	Name     *string
	Language *string
	TimeZone *string
	Currency *string
}

// UpdateHouseholdSettings changes the scope's own household. Validation of the values is the
// caller's (the GraphQL resolver checks the time zone and currency); the household is never
// taken from input.
func (c *Client) UpdateHouseholdSettings(ctx context.Context, s identity.Scope, in HouseholdSettings) (*models.Household, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, fmt.Errorf("a household needs a name")
		}
		updates["name"] = name
	}
	if in.Language != nil {
		updates["language"] = strings.TrimSpace(*in.Language)
	}
	if in.TimeZone != nil {
		updates["time_zone"] = strings.TrimSpace(*in.TimeZone)
	}
	if in.Currency != nil {
		updates["currency"] = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	res := c.DB.WithContext(ctx).Model(&models.Household{}).Where("id = ?", s.HouseholdID()).Updates(updates)
	if res.Error != nil {
		return nil, fmt.Errorf("update household: %w", res.Error)
	}
	return c.HouseholdByID(ctx, s.HouseholdID())
}

// UsersInHousehold returns the members of the scope's household.
func (c *Client) UsersInHousehold(ctx context.Context, s identity.Scope) ([]models.User, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	var users []models.User
	err := c.DB.WithContext(ctx).Where("household_id = ?", s.HouseholdID()).Order("created_at, platform_id").Find(&users).Error
	return users, err
}

// scoped starts a query confined to the scope's household. It refuses an invalid scope, so a
// method that forgot its scope fails instead of reading every household.
func (c *Client) scoped(s identity.Scope) (*gorm.DB, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	return c.DB.Where("household_id = ?", s.HouseholdID()), nil
}

// stamp writes the scope's household into a row about to be created or updated: household_id,
// and the legacy family_group label beside it until V4-44. Whatever the caller put there is
// overwritten, so a row can never be written into another household.
func stamp(s identity.Scope, householdID, familyGroup *string) error {
	if !s.Valid() {
		return identity.ErrNoScope
	}
	*householdID = s.HouseholdID()
	if familyGroup != nil {
		*familyGroup = s.LegacyLabel()
	}
	return nil
}

// householdTables lists the tables of the given models that carry household_id.
func householdTables(db *gorm.DB, all []interface{}) ([]string, error) {
	var out []string
	for _, m := range all {
		sch, err := schema.Parse(m, &sync.Map{}, db.NamingStrategy)
		if err != nil {
			return nil, err
		}
		if sch.LookUpField("HouseholdID") != nil {
			out = append(out, sch.Table)
		}
	}
	return out, nil
}

// applyHouseholdForeignKeys gives the AutoMigrate schema (tests and local experiments) the same
// household foreign keys migration 00003 creates. AutoMigrate creates no foreign key without a
// relation field, and the models carry none so that their JSON snapshots stay flat.
func applyHouseholdForeignKeys(db *gorm.DB, all []interface{}) error {
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	tables, err := householdTables(db, all)
	if err != nil {
		return fmt.Errorf("list household tables: %w", err)
	}
	for _, t := range tables {
		stmt := fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_%[1]s_household') THEN
				ALTER TABLE %[1]s ADD CONSTRAINT fk_%[1]s_household FOREIGN KEY (household_id) REFERENCES households(id);
			END IF;
		END $$;`, t)
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("household foreign key on %s: %w", t, err)
		}
	}
	return nil
}
