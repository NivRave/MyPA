package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/nivik/mypa/internal/models"
)

// ErrNotAMember is returned when a reference to another person does not name a member of the
// caller's household.
var ErrNotAMember = errors.New("identity: not a member of this household")

// Members lists the members of the scope's household. db.Client implements it.
type Members interface {
	UsersInHousehold(ctx context.Context, s Scope) ([]models.User, error)
}

// Member resolves a reference to another person (a tool argument such as assigned_to or
// shared_with, or the finance filter) inside the caller's household only (ADR 0002 Decision 3).
// The reference may be a member's platform ID or users.id; the result is the member's platform
// ID, the key the modules write. A reference outside the household is an error, never a guess.
func Member(ctx context.Context, m Members, s Scope, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !s.Valid() {
		return "", ErrNoScope
	}
	if ref == "" {
		return "", ErrNotAMember
	}
	if ref == s.PersonKey() {
		return ref, nil
	}
	if m == nil {
		return "", ErrNotAMember
	}
	users, err := m.UsersInHousehold(ctx, s)
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if u.HouseholdID != s.HouseholdID() {
			continue
		}
		if strings.TrimSpace(u.PlatformID) == ref || u.ID == ref {
			return strings.TrimSpace(u.PlatformID), nil
		}
	}
	return "", ErrNotAMember
}
