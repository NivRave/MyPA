package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR 0002 Decision 2 step 3, Migration C (V4-35): admin becomes owner and operator, family adult;
// a household with no admin gets no owner; outstanding pairing codes give adult, child or guest
// only; users.role is left as it was; and sessions exists.
func TestMigrationC_MovesRolesToMembershipsAndPeople(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	migrateTo(t, sqlDB, 4)

	// Migrations A and B already ran (versions 3 and 4) on an empty database; give these rows their
	// household, person and membership the way those migrations would have, with no role.
	household := map[string]string{}
	for _, label := range []string{"Levi", "2001", "Cohen"} {
		var id string
		require.NoError(t, sqlDB.QueryRow(`INSERT INTO households (name, legacy_label) VALUES ($1, $1) RETURNING id`, label).Scan(&id))
		household[label] = id
	}
	insertRow(t, sqlDB, "users", map[string]any{"platform_id": "1001", "name": "Dana", "role": "admin", "family_group": "Levi", "household_id": household["Levi"]})
	insertRow(t, sqlDB, "users", map[string]any{"platform_id": "1002", "name": "Omer", "role": "family", "family_group": "Levi", "household_id": household["Levi"]})
	insertRow(t, sqlDB, "users", map[string]any{"platform_id": "2001", "name": "Solo", "role": " Family ", "family_group": "", "household_id": household["2001"]})
	insertRow(t, sqlDB, "users", map[string]any{"platform_id": "3001", "name": "Odd", "role": "superuser", "family_group": "Cohen", "household_id": household["Cohen"]})
	for code, role := range map[string]string{"111111": "admin", "222222": "family", "333333": "child", "444444": "guest", "555555": "owner"} {
		insertRow(t, sqlDB, "pairing_codes", map[string]any{"code": code, "role": role, "family_group": "Levi", "household_id": household["Levi"],
			"expires_at": time.Now().Add(time.Hour), "is_used": false})
	}
	_, err := sqlDB.Exec(`
		INSERT INTO people (id, display_name) SELECT id, name FROM users;
		INSERT INTO memberships (person_id, household_id) SELECT id, household_id FROM users;`)
	require.NoError(t, err)

	migrateTo(t, sqlDB, 5)

	role := func(platformID string) (string, bool, string) {
		var r, legacy string
		var op bool
		require.NoError(t, sqlDB.QueryRow(`SELECT m.role, p.is_operator, u.role FROM users u
			JOIN memberships m ON m.person_id = u.id JOIN people p ON p.id = u.id WHERE u.platform_id = $1`, platformID).Scan(&r, &op, &legacy))
		return r, op, legacy
	}
	for platformID, want := range map[string]struct {
		role     string
		operator bool
		legacy   string
	}{
		"1001": {"owner", true, "admin"},
		"1002": {"adult", false, "family"},
		"2001": {"adult", false, " Family "},
		"3001": {"adult", false, "superuser"},
	} {
		r, op, legacy := role(platformID)
		assert.Equal(t, want.role, r, platformID)
		assert.Equal(t, want.operator, op, platformID)
		assert.Equal(t, want.legacy, legacy, "%s: users.role is left as it was", platformID)
	}

	var ownerless int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM households h WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.household_id = h.id)
		AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.household_id = h.id AND m.role = 'owner')`).Scan(&ownerless))
	assert.Equal(t, 2, ownerless, "the unlabelled user's household and Cohen have no admin, so no owner")

	codes := map[string]string{}
	rows, err := sqlDB.Query(`SELECT code, role FROM pairing_codes`)
	require.NoError(t, err)
	for rows.Next() {
		var c, r string
		require.NoError(t, rows.Scan(&c, &r))
		codes[c] = r
	}
	require.NoError(t, rows.Close())
	assert.Equal(t, map[string]string{"111111": "adult", "222222": "adult", "333333": "child", "444444": "guest", "555555": "adult"}, codes)

	_, err = sqlDB.Exec(`INSERT INTO memberships (person_id, household_id) SELECT id, household_id FROM users LIMIT 1`)
	assert.Error(t, err, "a membership without a role is refused from now on")
	var sessions bool
	require.NoError(t, sqlDB.QueryRow(`SELECT to_regclass('public.sessions') IS NOT NULL`).Scan(&sessions))
	assert.True(t, sessions)
}

// V4-35: the role and the operator flag are read from the membership and the person; users.role
// is written beside them and never read.
func TestAccessOf_ReadsTheMembershipNotUsersRole(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := personWithAccount(t, c, "Levi", "1001", "Dana", "admin", "telegram", time.Now().Add(-48*time.Hour))
	acc, err := c.AccessOf(ctx, u)
	require.NoError(t, err)
	assert.Equal(t, identity.RoleOwner, acc.Role)
	assert.True(t, acc.Operator)
	assert.Equal(t, u.HouseholdID, acc.HouseholdID)
	assert.Equal(t, "admin", u.Role, "users.role keeps the value the release before V4 reads")

	require.NoError(t, c.DB.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, u.ID).Error)
	require.NoError(t, c.DB.Exec(`UPDATE memberships SET role = 'guest' WHERE person_id = ?`, u.ID).Error)
	require.NoError(t, c.DB.Exec(`UPDATE people SET is_operator = false WHERE id = ?`, u.ID).Error)
	s := scopeOfUser(t, c, u)
	assert.Equal(t, identity.RoleGuest, s.Role(), "users.role says admin; the membership decides")
	assert.False(t, s.Can(identity.RunBackup))
	assert.False(t, s.Can(identity.SignInDashboard))

	require.NoError(t, c.DB.Exec(`DELETE FROM memberships WHERE person_id = ?`, u.ID).Error)
	_, err = identity.ForUser(ctx, c, u)
	assert.ErrorIs(t, err, identity.ErrNoScope, "a person removed from the household has no scope")
	_, err = c.AccessOf(ctx, &models.User{})
	assert.ErrorIs(t, err, identity.ErrNoScope)
}

// A pairing code gives adult, child or guest, never owner, whatever the code row or the caller
// says; users.role is written as the release before V4 reads it.
func TestRedeemPairingCode_GivesTheCodesRoleNeverOwner(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	owner, _ := personWithAccount(t, c, "Levi", "1001", "Dana", "admin", "telegram", time.Now())
	s := scopeOfUser(t, c, owner)

	for i, tc := range []struct{ code, want string }{{"child", "child"}, {"guest", "guest"}, {"adult", "adult"}, {"owner", "adult"}, {"admin", "adult"}} {
		pc := models.PairingCode{Code: "90000" + string(rune('0'+i)), Role: tc.code, ExpiresAt: time.Now().Add(time.Hour)}
		require.NoError(t, c.CreatePairingCode(s, pc))
		got, err := c.GetPairingCode(pc.Code)
		require.NoError(t, err)
		platformID := "77" + string(rune('0'+i))
		require.NoError(t, c.RedeemPairingCode(got.ID, models.User{PlatformID: platformID, Name: "New", Role: "admin"}, "telegram"))
		u, err := c.GetUser(platformID)
		require.NoError(t, err)
		acc, err := c.AccessOf(ctx, u)
		require.NoError(t, err)
		assert.Equal(t, identity.Role(tc.want), acc.Role, "code role %s", tc.code)
		assert.False(t, acc.Operator, "a code never makes an operator")
		assert.Equal(t, "family", u.Role, "code role %s: users.role is the legacy value", tc.code)
	}
}

// ADR 0002 Decision 6 with V4-35's roles: admin entries become owners and the operator; a household
// without one gets its first entry as owner and operator.
func TestBootstrap_AdminsBecomeOwnersAndOperators(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	entries := []models.User{
		{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"},
		{PlatformID: "1002", Name: "Omer", Role: "family", FamilyGroup: "Levi"},
		{PlatformID: "1003", Name: "Kid", Role: "child", FamilyGroup: "Levi"},
		{PlatformID: "2001", Name: "Noa", Role: "family", FamilyGroup: "Cohen"},
		{PlatformID: "2002", Name: "Ran", Role: "family", FamilyGroup: "Cohen"},
	}
	res, err := c.BootstrapHouseholds(ctx, entries, "UTC", func(string) string { return "telegram" })
	require.NoError(t, err)
	assert.Equal(t, []string{"2001"}, res.Promoted)

	for platformID, want := range map[string]identity.Access{
		"1001": {Role: identity.RoleOwner, Operator: true},
		"1002": {Role: identity.RoleAdult},
		"1003": {Role: identity.RoleChild},
		"2001": {Role: identity.RoleOwner, Operator: true},
		"2002": {Role: identity.RoleAdult},
	} {
		u, err := c.GetUser(platformID)
		require.NoError(t, err)
		acc, err := c.AccessOf(ctx, u)
		require.NoError(t, err)
		assert.Equal(t, want.Role, acc.Role, platformID)
		assert.Equal(t, want.Operator, acc.Operator, platformID)
		assert.Equal(t, identity.LegacyRole(want.Role), u.Role, platformID)
	}
	n, err := c.HouseholdsWithoutOwner(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
}

// An existing person keeps their role when a user write passes them again: only a grant changes
// a role.
func TestUpsertUser_DoesNotChangeAnExistingRole(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	u, _ := personWithAccount(t, c, "Levi", "1002", "Omer", "family", "telegram", time.Now())
	require.NoError(t, c.UpsertUser(models.User{PlatformID: "1002", Name: "Omer", Role: "admin", HouseholdID: u.HouseholdID, FamilyGroup: "Levi"}))
	acc, err := c.AccessOf(context.Background(), u)
	require.NoError(t, err)
	assert.Equal(t, identity.RoleAdult, acc.Role)
	assert.False(t, acc.Operator)
}

func TestMembersAndOwners_StayInsideTheHousehold(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	dana, _ := personWithAccount(t, c, "Levi", "1001", "Dana", "admin", "telegram", time.Now())
	personWithAccount(t, c, "Levi", "1002", "Omer", "family", "telegram", time.Now())
	personWithAccount(t, c, "Cohen", "2001", "Noa", "admin", "telegram", time.Now())
	s := scopeOfUser(t, c, dana)

	ms, err := c.MembersOf(ctx, s)
	require.NoError(t, err)
	roles := map[string]string{}
	for _, m := range ms {
		roles[m.PlatformID] = m.MemberRole
	}
	assert.Equal(t, map[string]string{"1001": "owner", "1002": "adult"}, roles)
	owners, err := c.HouseholdOwners(ctx, s)
	require.NoError(t, err)
	require.Len(t, owners, 1)
	assert.Equal(t, "1001", owners[0].PlatformID)
	_, err = c.MembersOf(ctx, identity.Scope{})
	assert.ErrorIs(t, err, identity.ErrNoScope)
}

// The offline recovery grant (ADR 0002 §10) gives owner or operator inside the person's own
// household, and /doctor's count of ownerless households follows it.
func TestGrantAccess_MakesAnOwnerOrTheOperator(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	omer, _ := personWithAccount(t, c, "Levi", "1002", "Omer", "family", "telegram", time.Now())
	n, err := c.HouseholdsWithoutOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	res, err := c.GrantAccess(ctx, "1002", GrantOwner)
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Equal(t, omer.ID, res.PersonID)
	assert.Equal(t, omer.HouseholdID, res.HouseholdID)
	res, err = c.GrantAccess(ctx, omer.ID, GrantOwner)
	require.NoError(t, err)
	assert.False(t, res.Changed, "already an owner")
	_, err = c.GrantAccess(ctx, "1002", GrantOperator)
	require.NoError(t, err)

	acc, err := c.AccessOf(ctx, omer)
	require.NoError(t, err)
	assert.Equal(t, identity.Access{HouseholdID: omer.HouseholdID, Role: identity.RoleOwner, Operator: true}, acc)
	u, err := c.GetUser("1002")
	require.NoError(t, err)
	assert.Equal(t, "admin", u.Role, "users.role follows for the release before V4")
	n, err = c.HouseholdsWithoutOwner(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	_, err = c.GrantAccess(ctx, "9999", GrantOwner)
	assert.Error(t, err, "nobody is known by that key")
	_, err = c.GrantAccess(ctx, "1002", Grant("root"))
	assert.Error(t, err)
}

func signInScope(t *testing.T, c *Client, platformID, role, label string) (identity.Scope, *models.User) {
	t.Helper()
	u, _ := personWithAccount(t, c, label, platformID, "Person "+platformID, role, "telegram", time.Now().Add(-48*time.Hour))
	return scopeOfUser(t, c, u), u
}

// ADR 0002 Decision 5: only the token's hash is stored; it works once, within 10 minutes; a new
// link voids the previous one; two browsers racing for one link cannot both get a session.
func TestSignIn_TokenIsSingleUseShortLivedAndHashed(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s, u := signInScope(t, c, "1001", "admin", "Levi")

	token, err := identity.NewSecret()
	require.NoError(t, err)
	tok, err := c.IssueSignIn(ctx, s, identity.HashSecret(token))
	require.NoError(t, err)
	var stored models.AccessToken
	require.NoError(t, c.DB.Where("id = ?", tok.ID).First(&stored).Error)
	assert.Equal(t, models.TokenPurposeSignin, stored.Purpose)
	assert.Equal(t, identity.HashSecret(token), stored.TokenHash)
	assert.NotContains(t, stored.TokenHash, token)
	assert.WithinDuration(t, time.Now().Add(identity.SignInTTL), stored.ExpiresAt, 5*time.Second)

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sec, _ := identity.NewSecret()
			_, _, err := c.RedeemSignIn(ctx, identity.HashSecret(token), identity.HashSecret(sec))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else {
			assert.ErrorIs(t, err, identity.ErrSignInInvalid)
		}
	}
	assert.Equal(t, 1, ok, "one link, one session")
	var sessions int64
	require.NoError(t, c.DB.Model(&models.Session{}).Where("person_id = ?", u.ID).Count(&sessions).Error)
	assert.Equal(t, int64(1), sessions)

	// A newer link voids the older one.
	first, _ := identity.NewSecret()
	second, _ := identity.NewSecret()
	_, err = c.IssueSignIn(ctx, s, identity.HashSecret(first))
	require.NoError(t, err)
	_, err = c.IssueSignIn(ctx, s, identity.HashSecret(second))
	require.NoError(t, err)
	sec, _ := identity.NewSecret()
	_, _, err = c.RedeemSignIn(ctx, identity.HashSecret(first), identity.HashSecret(sec))
	assert.ErrorIs(t, err, identity.ErrSignInInvalid, "the older link no longer works")

	// An expired link does not work.
	expired, _ := identity.NewSecret()
	tok, err = c.IssueSignIn(ctx, s, identity.HashSecret(expired))
	require.NoError(t, err)
	require.NoError(t, c.DB.Model(&models.AccessToken{}).Where("id = ?", tok.ID).Update("expires_at", time.Now().Add(-time.Second)).Error)
	_, _, err = c.RedeemSignIn(ctx, identity.HashSecret(expired), identity.HashSecret(sec))
	assert.ErrorIs(t, err, identity.ErrSignInInvalid)

	// A link code (another purpose) is never a sign-in token.
	linkHash := identity.HashCode(testCodeKey, "ABCDEFGH")
	require.NoError(t, c.DB.Create(&models.AccessToken{Purpose: models.TokenPurposeLink, TokenHash: linkHash, HouseholdID: s.HouseholdID(),
		PersonID: &u.ID, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}).Error)
	_, _, err = c.RedeemSignIn(ctx, linkHash, identity.HashSecret(sec))
	assert.ErrorIs(t, err, identity.ErrSignInInvalid)

	_, err = c.IssueSignIn(ctx, identity.Scope{}, identity.HashSecret(token))
	assert.ErrorIs(t, err, identity.ErrNoScope)
}

// ADR 0002 question 5: a session ends after 24 hours idle, after 7 days, when revoked, and on
// /signout; each request extends the idle limit.
func TestSession_IdleLifetimeAndRevocation(t *testing.T) {
	c, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s, u := signInScope(t, c, "1001", "family", "Levi")

	open := func() (string, string) {
		token, _ := identity.NewSecret()
		_, err := c.IssueSignIn(ctx, s, identity.HashSecret(token))
		require.NoError(t, err)
		secret, _ := identity.NewSecret()
		_, sess, err := c.RedeemSignIn(ctx, identity.HashSecret(token), identity.HashSecret(secret))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(identity.SessionMax), sess.ExpiresAt, 5*time.Second)
		return secret, sess.ID
	}

	secret, id := open()
	sid, pid, err := c.SessionPerson(ctx, identity.HashSecret(secret))
	require.NoError(t, err)
	assert.Equal(t, id, sid)
	assert.Equal(t, u.ID, pid)
	_, _, err = c.SessionPerson(ctx, secret)
	assert.ErrorIs(t, err, identity.ErrNoSession, "the secret itself is not what is stored")

	require.NoError(t, c.DB.Model(&models.Session{}).Where("id = ?", id).Update("last_seen_at", time.Now().Add(-23*time.Hour)).Error)
	_, _, err = c.SessionPerson(ctx, identity.HashSecret(secret))
	require.NoError(t, err, "23 hours idle is within the limit")
	var touched models.Session
	require.NoError(t, c.DB.Where("id = ?", id).First(&touched).Error)
	assert.WithinDuration(t, time.Now(), touched.LastSeenAt, 5*time.Second, "a request extends the idle limit")

	require.NoError(t, c.DB.Model(&models.Session{}).Where("id = ?", id).Update("last_seen_at", time.Now().Add(-25*time.Hour)).Error)
	_, _, err = c.SessionPerson(ctx, identity.HashSecret(secret))
	assert.ErrorIs(t, err, identity.ErrNoSession, "25 hours idle")

	secret, id = open()
	require.NoError(t, c.DB.Model(&models.Session{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Second)).Error)
	_, _, err = c.SessionPerson(ctx, identity.HashSecret(secret))
	assert.ErrorIs(t, err, identity.ErrNoSession, "past 7 days")

	secret, id = open()
	require.NoError(t, c.RevokeSession(ctx, id))
	_, _, err = c.SessionPerson(ctx, identity.HashSecret(secret))
	assert.ErrorIs(t, err, identity.ErrNoSession, "signed out")

	a, _ := open()
	b, _ := open()
	pending, _ := identity.NewSecret()
	_, err = c.IssueSignIn(ctx, s, identity.HashSecret(pending))
	require.NoError(t, err)
	n, err := c.RevokeSessionsOf(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	for _, sec := range []string{a, b} {
		_, _, err = c.SessionPerson(ctx, identity.HashSecret(sec))
		assert.ErrorIs(t, err, identity.ErrNoSession, "/signout ends every session")
	}
	_, _, err = c.RedeemSignIn(ctx, identity.HashSecret(pending), identity.HashSecret(a))
	assert.ErrorIs(t, err, identity.ErrSignInInvalid, "/signout voids an outstanding link too")

	purged, err := c.PurgeSignIns(ctx, -time.Minute)
	require.NoError(t, err)
	assert.Positive(t, purged)
	var left int64
	require.NoError(t, c.DB.Model(&models.Session{}).Count(&left).Error)
	assert.Zero(t, left, "every session ended, so the purge removed them")
}
