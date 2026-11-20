package models

import "time"

// HoldingHouseholdID is the household that owns rows written before V4-30 with an empty family
// label (ADR 0002, Decision 2). It has no members and no label, so no scope can ever be built for
// it: those rows stay as invisible as they were. Migration A creates it only when such rows exist.
const HoldingHouseholdID = "00000000-0000-0000-0000-000000000000"

// Household is a family: the tenant every household-scoped row belongs to (V4-30, ADR 0002).
// Family-level settings live here. An empty Language, TimeZone or Currency means "the install
// default" (DEFAULT_TIMEZONE, no language preference, USD), which is what every household
// migrated from a family label starts with.
type Household struct {
	ID   string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	Name string `json:"name" gorm:"not null"`
	// LegacyLabel is the family label (or, for a user who had none, their platform ID) that the
	// household replaces. It is written into family_group beside household_id until V4-44 drops
	// the old columns. NULL only for the holding household.
	LegacyLabel *string `json:"legacy_label,omitempty" gorm:"uniqueIndex"`
	Language    string  `json:"language" gorm:"not null;default:''"`
	TimeZone    string  `json:"time_zone" gorm:"not null;default:''"`
	Currency    string  `json:"currency" gorm:"not null;default:''"`
	// Packs are the opt-in feature packs the household has switched on (V4-27): pack IDs, sorted,
	// comma-separated; '' is Core only. Read it through identity.Scope.Packs (packs.Parse).
	Packs     string    `json:"packs" gorm:"not null;default:''"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
