// Package identity turns a verified caller into a Scope: the household, person and role that
// every repository call is checked against (V4-30, ADR 0002 Decision 3).
//
// A Scope has no exported fields, so it can only come from the constructors in this package, and
// each constructor reads the household from the database through a Store. The household is
// never taken from input: not from a tool argument, a GraphQL argument or a message.
//
// Until V4-32 and V4-35 the sources are the ones ADR 0002 §9 lists for V4-30: the sender's users
// row (an inbound message), the household whose label the dashboard caller is bound to, and a
// stored row's household_id (a scheduled job).
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nivik/mypa/internal/models"
)

// ErrNoScope is returned by every repository method called without a valid scope. A zero Scope
// is never valid, so a forgotten scope fails closed.
var ErrNoScope = errors.New("identity: no household scope")

// Audience says who a turn speaks to. V4-30 has direct chats only; group chats arrive with
// ADR 0003 and V4-16.
type Audience string

// Direct is a one-to-one chat with the person.
const Direct Audience = "direct"

// Store reads households. db.Client implements it; tests may use identitytest.Store.
type Store interface {
	// HouseholdByID returns the household, or nil and no error when there is none.
	HouseholdByID(ctx context.Context, id string) (*models.Household, error)
	// HouseholdsByLabel returns every household whose legacy label equals label, trimmed and
	// ignoring case (the dashboard's comparison, ADR 0002 §9).
	HouseholdsByLabel(ctx context.Context, label string) ([]models.Household, error)
}

// Scope is who is acting and in which household. The zero value is invalid.
type Scope struct {
	household models.Household
	personID  string
	personKey string
	role      Role
	operator  bool
	audience  Audience
}

// Valid reports whether the scope names a household a caller may act in.
func (s Scope) Valid() bool {
	return s.household.ID != "" && s.household.ID != models.HoldingHouseholdID
}

// HouseholdID is the tenant key every household-scoped query filters by.
func (s Scope) HouseholdID() string { return s.household.ID }

// LegacyLabel is the family label written into family_group beside household_id until V4-44
// drops the old column (ADR 0002 Decision 2: writes go to both keys through V4.0).
func (s Scope) LegacyLabel() string {
	if s.household.LegacyLabel == nil {
		return ""
	}
	return *s.household.LegacyLabel
}

// HouseholdName is the household's display name.
func (s Scope) HouseholdName() string { return s.household.Name }

// Household returns a copy of the household the scope is bound to.
func (s Scope) Household() models.Household {
	h := s.household
	if h.LegacyLabel != nil {
		l := *h.LegacyLabel
		h.LegacyLabel = &l
	}
	return h
}

// PersonKey is the acting person's key as the modules write it into the old person columns: the
// platform ID of the person's users row (the same whichever account they write from, V4-32), the
// dashboard's actor ID, or a stored row's user ID. The database fills the person_id beside it.
func (s Scope) PersonKey() string { return s.personKey }

// PersonID is the acting person's people.id (V4-32): the same for every channel account of one
// person. Empty for a dashboard caller (until V4-35 signs people in) and for a scheduled job.
func (s Scope) PersonID() string { return s.personID }

// Role is the acting person's role.
func (s Scope) Role() Role { return s.role }

// Audience is who the turn speaks to.
func (s Scope) Audience() Audience { return s.audience }

// Can reports whether the scope may perform the action. Every permission check goes through
// here; nothing compares role strings in place (ADR 0002 Decision 1).
func (s Scope) Can(a Action) bool {
	if !s.Valid() {
		return false
	}
	return allowed(s.role, s.operator, a)
}

// TimeZone is the household's time zone, or fallback (the install's DEFAULT_TIMEZONE) when the
// household has none.
func (s Scope) TimeZone(fallback string) string {
	if tz := strings.TrimSpace(s.household.TimeZone); tz != "" {
		return tz
	}
	return fallback
}

// Currency is the household's currency code, or fallback when it has none.
func (s Scope) Currency(fallback string) string {
	if c := strings.TrimSpace(s.household.Currency); c != "" {
		return c
	}
	return fallback
}

// Language is the household's language, or "" when it has none.
func (s Scope) Language() string { return strings.TrimSpace(s.household.Language) }

// ForUser builds the scope of a verified sender from their users row. The household is read
// from the database by the row's household_id. Since V4-32 the row is the person's own, found
// through the channel account the message came from, so every account of one person gets the
// same scope; the person is the row's ID (a person keeps users.id).
func ForUser(ctx context.Context, st Store, u *models.User) (Scope, error) {
	if u == nil {
		return Scope{}, fmt.Errorf("%w: no user", ErrNoScope)
	}
	h, err := load(ctx, st, u.HouseholdID)
	if err != nil {
		return Scope{}, err
	}
	role := ParseRole(u.Role)
	return Scope{household: *h, personID: strings.TrimSpace(u.ID), personKey: strings.TrimSpace(u.PlatformID), role: role, operator: role == RoleAdmin, audience: Direct}, nil
}

// ForLabel builds the scope of a dashboard caller bound to a family label (the dashboard secret's
// DASHBOARD_FAMILY_GROUP, or a signed token's family). The label is matched trimmed and ignoring
// case, as the dashboard compared it before V4-30; a label that matches two households is
// refused, because picking one would be guessing a tenant.
func ForLabel(ctx context.Context, st Store, label, actorID string, role Role) (Scope, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Scope{}, fmt.Errorf("%w: empty family label", ErrNoScope)
	}
	if st == nil {
		return Scope{}, fmt.Errorf("%w: no household store", ErrNoScope)
	}
	hs, err := st.HouseholdsByLabel(ctx, label)
	if err != nil {
		return Scope{}, fmt.Errorf("look up household: %w", err)
	}
	switch len(hs) {
	case 0:
		return Scope{}, fmt.Errorf("%w: no household for family %q", ErrNoScope, label)
	case 1:
	default:
		return Scope{}, fmt.Errorf("%w: family %q matches %d households; rename one", ErrNoScope, label, len(hs))
	}
	h := hs[0]
	if h.ID == "" || h.ID == models.HoldingHouseholdID {
		return Scope{}, fmt.Errorf("%w: household is not usable", ErrNoScope)
	}
	return Scope{household: h, personKey: strings.TrimSpace(actorID), role: role, operator: role == RoleAdmin, audience: Direct}, nil
}

// ForStoredRow builds the scope of a scheduled job from a stored row: the row's household_id
// and the person key it was written by. A job has no admin powers.
func ForStoredRow(ctx context.Context, st Store, householdID, personKey string) (Scope, error) {
	h, err := load(ctx, st, householdID)
	if err != nil {
		return Scope{}, err
	}
	return Scope{household: *h, personKey: strings.TrimSpace(personKey), role: RoleFamily, audience: Direct}, nil
}

func load(ctx context.Context, st Store, id string) (*models.Household, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: no household", ErrNoScope)
	}
	if id == models.HoldingHouseholdID {
		return nil, fmt.Errorf("%w: the holding household has no members", ErrNoScope)
	}
	if st == nil {
		return nil, fmt.Errorf("%w: no household store", ErrNoScope)
	}
	h, err := st.HouseholdByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("look up household: %w", err)
	}
	if h == nil || h.ID != id {
		return nil, fmt.Errorf("%w: household %s does not exist", ErrNoScope, id)
	}
	return h, nil
}
