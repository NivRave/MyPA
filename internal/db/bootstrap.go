package db

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

// BootstrapResult says what BootstrapHouseholds did.
type BootstrapResult struct {
	// Ignored is true when a household already existed, so the setting was not read.
	Ignored bool
	// Households and Users are how many rows were created.
	Households int
	Users      int
	// Promoted lists the platform IDs made admin because their household had none.
	Promoted []string
}

// BootstrapHouseholds seeds a fresh install from ALLOWED_USERS (ADR 0002 Decision 6, option 6b).
// It runs only when no household exists; afterwards the database is the single source of truth
// and the setting is ignored, which the start-up log and /doctor say.
//
//   - One household per label in the setting; an entry without a label gets a household of its
//     own, keyed by its platform ID like every unlabelled user before V4-30.
//   - Each admin entry stays an admin (in V4-30, admin is owner and operator). A household with
//     no admin gets its first entry, in the order written, as admin, with a warning.
//   - An empty setting creates nothing: the bot answers nobody until someone is added.
//
// New households start with timeZone (the install's DEFAULT_TIMEZONE) as their time zone.
func (c *Client) BootstrapHouseholds(ctx context.Context, entries []models.User, timeZone string) (BootstrapResult, error) {
	var res BootstrapResult
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize with a concurrent start of a second replica.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mypa_household_bootstrap'))`).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&models.Household{}).Where("id <> ?", models.HoldingHouseholdID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			res.Ignored = true
			return nil
		}

		type group struct {
			key     string
			members []models.User
		}
		var groups []*group
		byKey := map[string]*group{}
		for _, u := range entries {
			u.PlatformID = strings.TrimSpace(u.PlatformID)
			if u.PlatformID == "" {
				continue
			}
			u.FamilyGroup = strings.TrimSpace(u.FamilyGroup)
			key := u.FamilyGroup
			if key == "" {
				key = u.PlatformID
			}
			g, ok := byKey[key]
			if !ok {
				g = &group{key: key}
				byKey[key] = g
				groups = append(groups, g)
			}
			g.members = append(g.members, u)
		}

		for _, g := range groups {
			label := g.key
			name := label
			if g.members[0].FamilyGroup == "" && strings.TrimSpace(g.members[0].Name) != "" {
				name = strings.TrimSpace(g.members[0].Name)
			}
			h := models.Household{Name: name, LegacyLabel: &label, TimeZone: strings.TrimSpace(timeZone)}
			if err := tx.Create(&h).Error; err != nil {
				return fmt.Errorf("create household: %w", err)
			}
			res.Households++

			hasAdmin := false
			for _, m := range g.members {
				if strings.EqualFold(strings.TrimSpace(m.Role), "admin") {
					hasAdmin = true
					break
				}
			}
			for i, m := range g.members {
				if !hasAdmin && i == 0 {
					m.Role = "admin"
					res.Promoted = append(res.Promoted, m.PlatformID)
				}
				if strings.TrimSpace(m.Role) == "" {
					m.Role = "family"
				}
				m.HouseholdID = h.ID
				m.FamilyGroup = label // the legacy tenant key, written beside household_id until V4-44
				if m.CreatedAt.IsZero() {
					m.CreatedAt = time.Now()
				}
				if err := tx.Where("platform_id = ?", m.PlatformID).Assign(m).FirstOrCreate(&m).Error; err != nil {
					return fmt.Errorf("create user: %w", err)
				}
				res.Users++
			}
		}
		return nil
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("bootstrap households: %w", err)
	}
	return res, nil
}

// LogBootstrap writes the start-up lines for a bootstrap result (no personal data at INFO:
// counts only; the promoted entries are named by position, not by number).
func LogBootstrap(res BootstrapResult, settingSet bool) {
	switch {
	case res.Ignored && settingSet:
		slog.Info("ALLOWED_USERS is ignored: a household already exists, so the database is the source of truth; add people with a pairing code")
	case res.Ignored:
	case res.Households == 0:
		slog.Warn("no household exists and ALLOWED_USERS is empty: the bot answers nobody; set ALLOWED_USERS (id:name:role:family) and restart to create the first household and its owner")
	default:
		slog.Info("created the first households from ALLOWED_USERS", "households", res.Households, "users", res.Users)
		if len(res.Promoted) > 0 {
			slog.Warn("a household in ALLOWED_USERS had no admin: its first entry was made admin", "households_affected", len(res.Promoted))
		}
		if res.Households > 1 {
			slog.Warn("ALLOWED_USERS created more than one household; that is unusual for one install", "households", res.Households)
		}
	}
}
