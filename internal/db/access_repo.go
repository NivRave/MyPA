package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
)

// Roles, the operator flag and dashboard sign-in (V4-35, ADR 0002 Decisions 1 and 5). A person's
// role is their membership's, the operator flag is on their person row, and both are read on
// every message and every dashboard request, so a change takes effect at once. users.role is
// still written beside them (identity.LegacyRole) until V4-44, and never read.

// AccessOf implements identity.Store: the membership and person whose ID is u.ID. u.Role is not
// read.
func (c *Client) AccessOf(ctx context.Context, u *models.User) (identity.Access, error) {
	if u == nil || strings.TrimSpace(u.ID) == "" {
		return identity.Access{}, fmt.Errorf("%w: no person", identity.ErrNoScope)
	}
	var row struct {
		HouseholdID string
		Role        string
		IsOperator  bool
	}
	res := c.DB.WithContext(ctx).Raw(`
		SELECT m.household_id::text AS household_id, m.role, p.is_operator
		FROM memberships m JOIN people p ON p.id = m.person_id
		WHERE m.person_id = ?`, strings.TrimSpace(u.ID)).Scan(&row)
	if res.Error != nil {
		return identity.Access{}, fmt.Errorf("read access: %w", res.Error)
	}
	if res.RowsAffected == 0 || row.HouseholdID == "" {
		return identity.Access{}, fmt.Errorf("%w: the person has no membership", identity.ErrNoScope)
	}
	return identity.Access{HouseholdID: row.HouseholdID, Role: identity.ParseRole(row.Role), Operator: row.IsOperator}, nil
}

// UserByID returns the users row of the person with this ID (a person keeps users.id), or nil.
func (c *Client) UserByID(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	err := c.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

// Member is a users row with the role and operator flag of the person behind it.
type Member struct {
	models.User
	MemberRole string `gorm:"column:member_role"`
	Operator   bool   `gorm:"column:operator"`
}

// MembersOf lists the members of the scope's household with their roles, oldest first.
func (c *Client) MembersOf(ctx context.Context, s identity.Scope) ([]Member, error) {
	if !s.Valid() {
		return nil, identity.ErrNoScope
	}
	var out []Member
	err := c.DB.WithContext(ctx).Raw(`
		SELECT u.*, m.role AS member_role, p.is_operator AS operator
		FROM users u JOIN memberships m ON m.person_id = u.id JOIN people p ON p.id = u.id
		WHERE m.household_id = ? AND u.household_id = ?
		ORDER BY u.created_at, u.id`, s.HouseholdID(), s.HouseholdID()).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	return out, nil
}

// HouseholdOwners lists the owners of the scope's household (the people told about a change to a
// member's accounts, ADR 0002 Decision 4).
func (c *Client) HouseholdOwners(ctx context.Context, s identity.Scope) ([]models.User, error) {
	members, err := c.MembersOf(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []models.User
	for _, m := range members {
		if identity.ParseRole(m.MemberRole) == identity.RoleOwner {
			out = append(out, m.User)
		}
	}
	return out, nil
}

// HouseholdsWithoutOwner counts the households that have members but no owner. Migration C makes
// no owner where there was no admin; /doctor reports it, and the operator assigns one with
// `mypa access grant owner`.
func (c *Client) HouseholdsWithoutOwner(ctx context.Context) (int64, error) {
	var n int64
	err := c.DB.WithContext(ctx).Raw(`
		SELECT count(DISTINCT m.household_id) FROM memberships m
		WHERE NOT EXISTS (SELECT 1 FROM memberships o WHERE o.household_id = m.household_id AND o.role = 'owner')`).Scan(&n).Error
	if err != nil {
		return 0, fmt.Errorf("households without an owner: %w", err)
	}
	return n, nil
}

// Grant is what GrantAccess may give.
type Grant string

const (
	// GrantOwner makes the person an owner of their household.
	GrantOwner Grant = "owner"
	// GrantOperator makes the person the install's operator.
	GrantOperator Grant = "operator"
)

// GrantResult says whom a grant reached.
type GrantResult struct {
	PersonID    string
	HouseholdID string
	Changed     bool
}

// GrantAccess is the offline recovery path (ADR 0002 §10): it makes the person behind key (a
// platform ID, a channel account's external ID, or a person ID) an owner of their own household,
// or the operator. It never moves anyone to another household. users.role is written beside it.
func (c *Client) GrantAccess(ctx context.Context, key string, g Grant) (GrantResult, error) {
	var out GrantResult
	if g != GrantOwner && g != GrantOperator {
		return out, fmt.Errorf("unknown grant %q: owner or operator", g)
	}
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var personID *string
		if err := tx.Raw("SELECT public.mypa_person_of(?)::text", strings.TrimSpace(key)).Scan(&personID).Error; err != nil {
			return err
		}
		if personID == nil || *personID == "" {
			return fmt.Errorf("no person is known by %q", key)
		}
		out.PersonID = *personID
		var m models.Membership
		if err := tx.Where("person_id = ?", out.PersonID).First(&m).Error; err != nil {
			return fmt.Errorf("the person has no household")
		}
		out.HouseholdID = m.HouseholdID
		if g == GrantOperator {
			res := tx.Model(&models.Person{}).Where("id = ? AND NOT is_operator", out.PersonID).Update("is_operator", true)
			out.Changed = res.RowsAffected > 0
			return res.Error
		}
		res := tx.Model(&models.Membership{}).Where("person_id = ? AND role <> ?", out.PersonID, string(identity.RoleOwner)).
			Update("role", string(identity.RoleOwner))
		if res.Error != nil {
			return res.Error
		}
		out.Changed = res.RowsAffected > 0
		return tx.Model(&models.User{}).Where("id = ?", out.PersonID).Update("role", identity.LegacyRole(identity.RoleOwner)).Error
	})
	return out, err
}

// IssueSignIn stores a new dashboard sign-in token for the scope's person, replacing any they
// still had outstanding. Only the token's SHA-256 is stored. The caller checks that the scope may
// sign in (identity.SignInDashboard) and the account's age.
func (c *Client) IssueSignIn(ctx context.Context, s identity.Scope, tokenHash string) (*models.AccessToken, error) {
	if !s.Valid() || s.PersonID() == "" {
		return nil, identity.ErrNoScope
	}
	if strings.TrimSpace(tokenHash) == "" {
		return nil, fmt.Errorf("issue sign-in: no token hash")
	}
	person := s.PersonID()
	tok := models.AccessToken{
		Purpose:     models.TokenPurposeSignin,
		TokenHash:   tokenHash,
		HouseholdID: s.HouseholdID(),
		PersonID:    &person,
		CreatedBy:   &person,
		ExpiresAt:   time.Now().Add(identity.SignInTTL),
		CreatedAt:   time.Now(),
	}
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.AccessToken{}).
			Where("purpose = ? AND person_id = ? AND used_at IS NULL", models.TokenPurposeSignin, person).
			Update("used_at", time.Now()).Error; err != nil {
			return err
		}
		return tx.Create(&tok).Error
	})
	if err != nil {
		return nil, fmt.Errorf("issue sign-in: %w", err)
	}
	return &tok, nil
}

// RedeemSignIn turns a sign-in token into a session. The token is used in one statement (unused,
// unexpired, a sign-in token, returning the row), so two browsers racing for one link cannot both
// get a session. The session's secret is stored as its hash only; remoteAddress is the browser's
// address, kept on the session as the sign-in's audit record.
func (c *Client) RedeemSignIn(ctx context.Context, tokenHash, sessionHash, remoteAddress string) (*models.AccessToken, *models.Session, error) {
	if strings.TrimSpace(tokenHash) == "" || strings.TrimSpace(sessionHash) == "" {
		return nil, nil, identity.ErrSignInInvalid
	}
	var tok models.AccessToken
	var sess models.Session
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Raw(`
			UPDATE access_tokens SET used_at = now()
			WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > now() AND person_id IS NOT NULL
			RETURNING *`, tokenHash, models.TokenPurposeSignin).Scan(&tok)
		if res.Error != nil {
			return res.Error
		}
		if tok.ID == "" || tok.PersonID == nil {
			return identity.ErrSignInInvalid
		}
		now := time.Now()
		sess = models.Session{SecretHash: sessionHash, PersonID: *tok.PersonID, CreatedAt: now, LastSeenAt: now,
			ExpiresAt: now.Add(identity.SessionMax), RemoteAddress: strings.TrimSpace(remoteAddress)}
		return tx.Create(&sess).Error
	})
	if err != nil {
		if errors.Is(err, identity.ErrSignInInvalid) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("redeem sign-in: %w", err)
	}
	return &tok, &sess, nil
}

// SessionPerson returns the session and its person for a session secret's hash, and records the
// request. One statement checks and touches it: not revoked, within its lifetime, and used within
// the idle limit; anything else is identity.ErrNoSession.
func (c *Client) SessionPerson(ctx context.Context, sessionHash string) (sessionID, personID string, err error) {
	if strings.TrimSpace(sessionHash) == "" {
		return "", "", identity.ErrNoSession
	}
	var row struct {
		ID       string
		PersonID string
	}
	res := c.DB.WithContext(ctx).Raw(`
		UPDATE sessions SET last_seen_at = now()
		WHERE secret_hash = ? AND revoked_at IS NULL AND expires_at > now() AND last_seen_at > ?
		RETURNING id::text AS id, person_id::text AS person_id`, sessionHash, time.Now().Add(-identity.SessionIdle)).Scan(&row)
	if res.Error != nil {
		return "", "", fmt.Errorf("read session: %w", res.Error)
	}
	if row.ID == "" {
		return "", "", identity.ErrNoSession
	}
	return row.ID, row.PersonID, nil
}

// RevokeSession ends one session (signing out in the dashboard).
func (c *Client) RevokeSession(ctx context.Context, sessionID string) error {
	return c.DB.WithContext(ctx).Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).Update("revoked_at", time.Now()).Error
}

// RevokeSessionsOf ends every session of the person (/signout in chat) and voids their
// outstanding sign-in links. It returns how many sessions it ended.
func (c *Client) RevokeSessionsOf(ctx context.Context, personID string) (int64, error) {
	var n int64
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.Session{}).
			Where("person_id = ? AND revoked_at IS NULL AND expires_at > now() AND last_seen_at > ?", personID, time.Now().Add(-identity.SessionIdle)).
			Update("revoked_at", time.Now())
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		return tx.Model(&models.AccessToken{}).
			Where("purpose = ? AND person_id = ? AND used_at IS NULL", models.TokenPurposeSignin, personID).
			Update("used_at", time.Now()).Error
	})
	return n, err
}

// PurgeSignIns deletes sessions that ended and sign-in links that were used or expired more than
// keep ago (the clean-up ADR 0002 Decision 5 asks for). An ended session is the sign-in's audit
// record, so the worker keeps them 30 days. It returns how many rows it deleted.
func (c *Client) PurgeSignIns(ctx context.Context, keep time.Duration) (int64, error) {
	cutoff := time.Now().Add(-keep)
	var total int64
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`DELETE FROM sessions WHERE revoked_at < ? OR expires_at < ? OR last_seen_at < ?`,
			cutoff, cutoff, cutoff.Add(-identity.SessionIdle))
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected
		res = tx.Exec(`DELETE FROM access_tokens WHERE purpose = ? AND (used_at < ? OR expires_at < ?)`,
			models.TokenPurposeSignin, cutoff, cutoff)
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("purge sign-ins: %w", err)
	}
	return total, nil
}
