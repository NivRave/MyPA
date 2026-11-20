package db

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/identity"
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
	// Promoted lists the platform IDs made owner and operator because their household had no owner.
	Promoted []string
	// NoChannel counts the entries whose channel could not be told, so they have no account yet.
	NoChannel int
}

// BootstrapHouseholds seeds a fresh install from ALLOWED_USERS (ADR 0002 Decision 6, option 6b).
// It runs only when no household exists; afterwards the database is the single source of truth
// and the setting is ignored, which the start-up log and /doctor say.
//
//   - One household per label in the setting; an entry without a label gets a household of its
//     own, keyed by its platform ID like every unlabelled user before V4-30.
//   - Each admin entry becomes an owner and the operator (V4-35); "owner", "adult", "child" and
//     "guest" are read as such, and "family" or no role is an adult. A household with no admin or
//     owner gets its first entry, in the order written, as owner and operator, with a warning.
//   - An empty setting creates nothing: the bot answers nobody until someone is added.
//
// New households start with timeZone (the install's DEFAULT_TIMEZONE) as their time zone.
//
// Each entry becomes a person with one channel account (V4-32): channelOf names the channel of a
// platform ID (the entry's fifth field, or the adapter that recognizes the ID); an entry whose
// channel is unknown gets no account and is logged.
func (c *Client) BootstrapHouseholds(ctx context.Context, entries []models.User, timeZone string, channelOf func(platformID string) string) (BootstrapResult, error) {
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

			hasOwner := false
			for _, m := range g.members {
				if r, _ := entryRole(m.Role); r == identity.RoleOwner {
					hasOwner = true
					break
				}
			}
			for i, m := range g.members {
				if !hasOwner && i == 0 {
					m.Role = "admin"
					res.Promoted = append(res.Promoted, m.PlatformID)
				}
				role, operator := entryRole(m.Role)
				m.Role = identity.LegacyRole(role)
				m.HouseholdID = h.ID
				m.FamilyGroup = label // the legacy tenant key, written beside household_id until V4-44
				if m.CreatedAt.IsZero() {
					m.CreatedAt = time.Now()
				}
				if err := tx.Where("platform_id = ?", m.PlatformID).Assign(m).FirstOrCreate(&m).Error; err != nil {
					return fmt.Errorf("create user: %w", err)
				}
				ch := ""
				if channelOf != nil {
					ch = channelOf(m.PlatformID)
				}
				if ch == "" {
					res.NoChannel++
				}
				if err := ensurePerson(tx, m, ch, role, operator); err != nil {
					return err
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
			slog.Warn("a household in ALLOWED_USERS had no admin: its first entry was made its owner and the operator", "households_affected", len(res.Promoted))
		}
		if res.NoChannel > 0 {
			slog.Warn("ALLOWED_USERS entries whose channel is unknown got no channel account and cannot write yet; add the channel as a fifth field (id:name:role:family:channel)", "entries", res.NoChannel)
		}
		if res.Households > 1 {
			slog.Warn("ALLOWED_USERS created more than one household; that is unusual for one install", "households", res.Households)
		}
	}
}
