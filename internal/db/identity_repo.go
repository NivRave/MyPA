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
	"gorm.io/gorm/clause"
)

// People and channel accounts (V4-32, ADR 0002 Decisions 1 and 4). A channel account resolves to
// its person; the person keeps the users row's ID, and until V4-35 the role is still that users
// row's. Every account of one person therefore resolves to the same users row, so the same
// household, the same person key and the same data whichever channel a message came from.

// UserForAccount returns the users row of the person who owns the channel account, or
// gorm.ErrRecordNotFound when the account belongs to nobody.
func (c *Client) UserForAccount(ctx context.Context, channelName, externalID string) (*models.User, error) {
	var u models.User
	err := c.DB.WithContext(ctx).
		Joins("JOIN channel_accounts ca ON ca.person_id = users.id").
		Where("ca.channel = ? AND ca.external_id = ?", channelName, externalID).
		First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AccountOf returns the channel account, or nil when it belongs to nobody.
func (c *Client) AccountOf(ctx context.Context, channelName, externalID string) (*models.ChannelAccount, error) {
	var a models.ChannelAccount
	err := c.DB.WithContext(ctx).Where("channel = ? AND external_id = ?", channelName, externalID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get channel account: %w", err)
	}
	return &a, nil
}

// RecordInbound notes that the account just wrote (last_inbound_at). Only a real inbound message
// counts; a briefing the assistant starts itself does not.
func (c *Client) RecordInbound(ctx context.Context, channelName, externalID string) error {
	return c.DB.WithContext(ctx).Model(&models.ChannelAccount{}).
		Where("channel = ? AND external_id = ?", channelName, externalID).
		Update("last_inbound_at", time.Now()).Error
}

// PersonOfAccount returns the person who owns the account, or "" when nobody does. The inbound
// queue uses it to keep one processing lane per person.
func (c *Client) PersonOfAccount(ctx context.Context, channelName, externalID string) (string, error) {
	a, err := c.AccountOf(ctx, channelName, externalID)
	if err != nil || a == nil {
		return "", err
	}
	return a.PersonID, nil
}

// PersonOfKey maps an old person key (a platform ID, or users.id) to its person, or "".
func (c *Client) PersonOfKey(ctx context.Context, key string) (string, error) {
	var id *string
	if err := c.DB.WithContext(ctx).Raw("SELECT public.mypa_person_of(?)::text", key).Scan(&id).Error; err != nil {
		return "", fmt.Errorf("person of key: %w", err)
	}
	if id == nil {
		return "", nil
	}
	return *id, nil
}

// PersonByID returns the person, or nil when there is none.
func (c *Client) PersonByID(ctx context.Context, id string) (*models.Person, error) {
	var p models.Person
	err := c.DB.WithContext(ctx).Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get person: %w", err)
	}
	return &p, nil
}

// AccountsOfPerson lists the person's channel accounts, oldest first.
func (c *Client) AccountsOfPerson(ctx context.Context, personID string) ([]models.ChannelAccount, error) {
	var out []models.ChannelAccount
	err := c.DB.WithContext(ctx).Where("person_id = ?", personID).Order("linked_at, id").Find(&out).Error
	return out, err
}

// DeliveryAccount is where a message the assistant starts for the person behind key goes: an
// account the person has written from (as before V4-32, when their latest session chose the
// channel), the preferred one first, else the one they wrote from last. nil when key names nobody
// or the person never wrote.
func (c *Client) DeliveryAccount(ctx context.Context, key string) (*models.ChannelAccount, error) {
	var a models.ChannelAccount
	err := c.DB.WithContext(ctx).Raw(`
		SELECT ca.* FROM channel_accounts ca JOIN people p ON p.id = ca.person_id
		WHERE p.id = (SELECT public.mypa_person_of(?)) AND ca.last_inbound_at IS NOT NULL
		ORDER BY (ca.id = p.preferred_account_id) IS TRUE DESC, ca.last_inbound_at DESC, ca.id
		LIMIT 1`, key).Scan(&a).Error
	if err != nil {
		return nil, fmt.Errorf("delivery account: %w", err)
	}
	if a.ID == "" {
		return nil, nil
	}
	return &a, nil
}

// AccountKeysOf returns the external IDs of the accounts of the person behind key, preferred
// first. Google sign-ins keep their platform-ID keys until V4-23 and are found through these.
func (c *Client) AccountKeysOf(ctx context.Context, key string) ([]string, error) {
	var out []string
	err := c.DB.WithContext(ctx).Raw(`
		SELECT ca.external_id FROM channel_accounts ca JOIN people p ON p.id = ca.person_id
		WHERE p.id = (SELECT public.mypa_person_of(?))
		ORDER BY (ca.id = p.preferred_account_id) IS TRUE DESC, ca.linked_at, ca.id`, key).Scan(&out).Error
	return out, err
}

// SetPreferredAccount makes one of the person's accounts the one notifications go to.
func (c *Client) SetPreferredAccount(ctx context.Context, personID, accountID string) error {
	res := c.DB.WithContext(ctx).Exec(`UPDATE people SET preferred_account_id = ?, updated_at = now()
		WHERE id = ? AND EXISTS (SELECT 1 FROM channel_accounts WHERE id = ? AND person_id = ?)`,
		accountID, personID, accountID, personID)
	if res.Error != nil {
		return fmt.Errorf("set preferred account: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return identity.ErrNoSuchAccount
	}
	return nil
}

// RemoveAccount removes one of the person's accounts on behalf of another (or the same) account
// of that person: an account may remove itself; removing another needs an older account; the
// last account is never removed (ADR 0002 Decision 4). Owners removing a member's account come
// with roles (V4-35).
func (c *Client) RemoveAccount(ctx context.Context, actorAccountID, targetAccountID string) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var actor models.ChannelAccount
		if err := tx.Where("id = ?", actorAccountID).First(&actor).Error; err != nil {
			return identity.ErrNoSuchAccount
		}
		var accounts []models.ChannelAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("person_id = ?", actor.PersonID).Order("linked_at, id").Find(&accounts).Error; err != nil {
			return err
		}
		var target *models.ChannelAccount
		for i := range accounts {
			if accounts[i].ID == targetAccountID {
				target = &accounts[i]
			}
		}
		switch {
		case target == nil:
			return identity.ErrNoSuchAccount
		case len(accounts) <= 1:
			return identity.ErrLastAccount
		case target.ID != actor.ID && !actor.LinkedAt.Before(target.LinkedAt):
			return identity.ErrNotOlder
		}
		return tx.Where("id = ? AND person_id = ?", target.ID, actor.PersonID).Delete(&models.ChannelAccount{}).Error
	})
}

// IssueLinkCode stores a new link code for the issuing account's person, inside the scope's
// household, replacing any code that person still had outstanding. Only the code's keyed hash is
// stored. The caller checks the account's age (identity.MayIssue).
func (c *Client) IssueLinkCode(ctx context.Context, s identity.Scope, issuer models.ChannelAccount, codeHash string) error {
	if !s.Valid() {
		return identity.ErrNoScope
	}
	if strings.TrimSpace(codeHash) == "" || issuer.ID == "" || issuer.PersonID == "" {
		return fmt.Errorf("issue link code: missing hash or issuer")
	}
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m models.Membership
		if err := tx.Where("person_id = ? AND household_id = ?", issuer.PersonID, s.HouseholdID()).First(&m).Error; err != nil {
			return fmt.Errorf("issue link code: the issuer is not a member of this household")
		}
		if err := tx.Model(&models.AccessToken{}).
			Where("purpose = ? AND person_id = ? AND used_at IS NULL", models.TokenPurposeLink, issuer.PersonID).
			Update("used_at", time.Now()).Error; err != nil {
			return err
		}
		person, account := issuer.PersonID, issuer.ID
		tok := models.AccessToken{
			Purpose:           models.TokenPurposeLink,
			TokenHash:         codeHash,
			HouseholdID:       s.HouseholdID(),
			PersonID:          &person,
			CreatedBy:         &person,
			IssuedOnAccountID: &account,
			ExpiresAt:         time.Now().Add(identity.LinkCodeTTL),
			CreatedAt:         time.Now(),
		}
		return tx.Create(&tok).Error
	})
}

// ClaimLinkCode records that an account typed a link code. It is one statement: the code is
// claimed only if it is a link code that is unclaimed, unused and unexpired, so two accounts
// racing for one code cannot both claim it. A claimed code links nothing until the issuing
// account confirms (ConfirmLink, MergeOnLink).
func (c *Client) ClaimLinkCode(ctx context.Context, codeHash, channelName, externalID string) (*models.AccessToken, error) {
	var tok models.AccessToken
	res := c.DB.WithContext(ctx).Raw(`
		UPDATE access_tokens SET claimed_by_channel = ?, claimed_by_external_id = ?, claimed_at = now()
		WHERE token_hash = ? AND purpose = ? AND claimed_at IS NULL AND used_at IS NULL AND expires_at > now()
		RETURNING *`, channelName, externalID, codeHash, models.TokenPurposeLink).Scan(&tok)
	if res.Error != nil {
		return nil, fmt.Errorf("claim link code: %w", res.Error)
	}
	if tok.ID == "" {
		return nil, identity.ErrCodeInvalid
	}
	return &tok, nil
}

// PendingLink returns the code typed on another account that waits for this account's
// confirmation, or identity.ErrNothingPending.
func (c *Client) PendingLink(ctx context.Context, issuerAccountID string) (*models.AccessToken, error) {
	var tok models.AccessToken
	err := c.DB.WithContext(ctx).
		Where("purpose = ? AND issued_on_account_id = ? AND claimed_at IS NOT NULL AND used_at IS NULL AND issuer_confirmed_at IS NULL AND claimed_at > ?",
			models.TokenPurposeLink, issuerAccountID, time.Now().Add(-identity.LinkCodeTTL)).
		Order("claimed_at DESC").First(&tok).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, identity.ErrNothingPending
	}
	if err != nil {
		return nil, fmt.Errorf("pending link: %w", err)
	}
	return &tok, nil
}

// useLinkCode marks a claimed code used, in one statement, only from its issuing account and
// within the confirmation window.
func useLinkCode(tx *gorm.DB, tokenID, issuerAccountID string) (*models.AccessToken, error) {
	var tok models.AccessToken
	res := tx.Raw(`
		UPDATE access_tokens SET used_at = now()
		WHERE id = ? AND purpose = ? AND issued_on_account_id = ? AND used_at IS NULL
		  AND issuer_confirmed_at IS NULL AND claimed_at IS NOT NULL AND claimed_at > ?
		RETURNING *`, tokenID, models.TokenPurposeLink, issuerAccountID, time.Now().Add(-identity.LinkCodeTTL)).Scan(&tok)
	if res.Error != nil {
		return nil, res.Error
	}
	if tok.ID == "" || tok.PersonID == nil || tok.ClaimedByChannel == nil || tok.ClaimedByExternalID == nil {
		return nil, identity.ErrNothingPending
	}
	return &tok, nil
}

// ConfirmLink adds the account that typed the code to the issuing person. The code is used in the
// same transaction; if the account joined someone meanwhile, nothing is linked.
func (c *Client) ConfirmLink(ctx context.Context, tokenID, issuerAccountID string) (*models.ChannelAccount, error) {
	var added models.ChannelAccount
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tok, err := useLinkCode(tx, tokenID, issuerAccountID)
		if err != nil {
			return err
		}
		var existing int64
		if err := tx.Model(&models.ChannelAccount{}).
			Where("channel = ? AND external_id = ?", *tok.ClaimedByChannel, *tok.ClaimedByExternalID).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return identity.ErrAccountTaken
		}
		added = models.ChannelAccount{
			PersonID:      *tok.PersonID,
			Channel:       *tok.ClaimedByChannel,
			ExternalID:    *tok.ClaimedByExternalID,
			LinkedAt:      time.Now(),
			LastInboundAt: tok.ClaimedAt, // the account wrote when it typed the code
		}
		return tx.Create(&added).Error
	})
	if err != nil {
		return nil, err
	}
	return &added, nil
}

// ConfirmMergeByIssuer records the issuing account's confirmation of a merge: the account that
// typed the code already is a person. It merges nothing: the merge needs that account's own
// confirmation too (MergeOnLink), so nobody can pull another person's account into theirs by
// getting them to type a code (ADR 0002 Decision 4). One statement, only from the issuing account
// and within the window.
func (c *Client) ConfirmMergeByIssuer(ctx context.Context, tokenID, issuerAccountID string) (*models.AccessToken, error) {
	var tok models.AccessToken
	res := c.DB.WithContext(ctx).Raw(`
		UPDATE access_tokens SET issuer_confirmed_at = now()
		WHERE id = ? AND purpose = ? AND issued_on_account_id = ? AND used_at IS NULL
		  AND issuer_confirmed_at IS NULL AND claimed_at IS NOT NULL AND claimed_at > ?
		RETURNING *`, tokenID, models.TokenPurposeLink, issuerAccountID, time.Now().Add(-identity.LinkCodeTTL)).Scan(&tok)
	if res.Error != nil {
		return nil, fmt.Errorf("confirm merge: %w", res.Error)
	}
	if tok.ID == "" {
		return nil, identity.ErrNothingPending
	}
	return &tok, nil
}

// PendingMerge returns the merge the issuer confirmed that waits for this account's own
// confirmation, or identity.ErrNothingPending.
func (c *Client) PendingMerge(ctx context.Context, channelName, externalID string) (*models.AccessToken, error) {
	var tok models.AccessToken
	err := c.DB.WithContext(ctx).
		Where("purpose = ? AND claimed_by_channel = ? AND claimed_by_external_id = ? AND used_at IS NULL AND issuer_confirmed_at > ?",
			models.TokenPurposeLink, channelName, externalID, time.Now().Add(-identity.LinkCodeTTL)).
		Order("issuer_confirmed_at DESC").First(&tok).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, identity.ErrNothingPending
	}
	if err != nil {
		return nil, fmt.Errorf("pending merge: %w", err)
	}
	return &tok, nil
}

// MergeOnLink makes the person who typed the code part of the issuing person (ADR 0002 Decision 4,
// question 4: only when that human links them, inside one household, after a backup — the caller
// takes the backup first). It runs only on the confirmation of the account that typed the code,
// after the issuing account confirmed (ConfirmMergeByIssuer): both sides. In one transaction: the
// code is used, the merged person's accounts and rows move to the survivor (the issuer), the rows
// only one of them can keep are resolved (DP-27), and the merged person and their users row are
// removed.
func (c *Client) MergeOnLink(ctx context.Context, tokenID, claimantChannel, claimantExternalID string) (*identity.MergeResult, error) {
	var out identity.MergeResult
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tok models.AccessToken
		res := tx.Raw(`
			UPDATE access_tokens SET used_at = now()
			WHERE id = ? AND purpose = ? AND used_at IS NULL
			  AND claimed_by_channel = ? AND claimed_by_external_id = ? AND issuer_confirmed_at > ?
			RETURNING *`, tokenID, models.TokenPurposeLink, claimantChannel, claimantExternalID,
			time.Now().Add(-identity.LinkCodeTTL)).Scan(&tok)
		if res.Error != nil {
			return res.Error
		}
		if tok.ID == "" || tok.PersonID == nil || tok.ClaimedByChannel == nil || tok.ClaimedByExternalID == nil {
			return identity.ErrNothingPending
		}
		var claimed models.ChannelAccount
		if err := tx.Where("channel = ? AND external_id = ?", *tok.ClaimedByChannel, *tok.ClaimedByExternalID).
			First(&claimed).Error; err != nil {
			return identity.ErrNothingPending
		}
		survivor, merged := *tok.PersonID, claimed.PersonID
		if survivor == merged {
			out = identity.MergeResult{Survivor: survivor, Merged: merged}
			return nil
		}
		var hs []models.Membership
		if err := tx.Where("person_id IN ?", []string{survivor, merged}).Find(&hs).Error; err != nil {
			return err
		}
		if len(hs) != 2 || hs[0].HouseholdID != hs[1].HouseholdID || hs[0].HouseholdID != tok.HouseholdID {
			return identity.ErrOtherHousehold
		}
		out = identity.MergeResult{Survivor: survivor, Merged: merged}

		res = tx.Model(&models.ChannelAccount{}).Where("person_id = ?", merged).Update("person_id", survivor)
		if res.Error != nil {
			return res.Error
		}
		out.Accounts = res.RowsAffected

		if err := resolveOnePerPerson(tx, survivor, merged); err != nil {
			return err
		}
		for _, col := range personColumns() {
			res := tx.Exec(fmt.Sprintf("UPDATE public.%[1]s SET %[2]s = ? WHERE %[2]s = ?", col.Table, col.Person), survivor, merged)
			if res.Error != nil {
				return fmt.Errorf("move %s.%s: %w", col.Table, col.Person, res.Error)
			}
			out.Rows += res.RowsAffected
		}
		if err := tx.Exec(`DELETE FROM people WHERE id = ?`, merged).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM users WHERE id = ?`, merged).Error
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// resolveOnePerPerson settles the rows a person has only one of, before a merge moves the rest:
// the survivor's preferences win and the merged person's fill only the fields the survivor left
// empty; per-topic calendar settings the survivor already has win, the merged person's other
// topics move. What does not survive is deleted, after the backup the caller took.
func resolveOnePerPerson(tx *gorm.DB, survivor, merged string) error {
	stmts := []string{
		`UPDATE user_preferences s SET
			preferred_language = COALESCE(NULLIF(s.preferred_language, ''), m.preferred_language),
			tone_persona = COALESCE(NULLIF(s.tone_persona, ''), m.tone_persona),
			default_currency = COALESCE(NULLIF(s.default_currency, ''), m.default_currency),
			quiet_hours_start = COALESCE(NULLIF(s.quiet_hours_start, ''), m.quiet_hours_start),
			quiet_hours_end = COALESCE(NULLIF(s.quiet_hours_end, ''), m.quiet_hours_end),
			standing_instructions = COALESCE(NULLIF(s.standing_instructions, ''), m.standing_instructions)
		FROM user_preferences m WHERE s.person_id = @survivor AND m.person_id = @merged`,
		`DELETE FROM user_preferences m WHERE m.person_id = @merged
			AND EXISTS (SELECT 1 FROM user_preferences s WHERE s.person_id = @survivor)`,
		`DELETE FROM topic_calendar_configs m WHERE m.person_id = @merged
			AND EXISTS (SELECT 1 FROM topic_calendar_configs s WHERE s.person_id = @survivor AND lower(s.topic) = lower(m.topic))`,
	}
	for _, s := range stmts {
		if err := tx.Exec(s, map[string]interface{}{"survivor": survivor, "merged": merged}).Error; err != nil {
			return fmt.Errorf("resolve one-per-person rows: %w", err)
		}
	}
	return nil
}

// ResolveAccountChannels fills the channel of every account that has none, by asking channelOf
// (the app asks the registered adapters which of them recognizes the ID, the rule the code used
// before V4-32). Migration B leaves the channel empty because no code outside the adapters names a
// platform. It returns how many were filled and how many no adapter recognized; the app runs it at
// start-up, before it takes messages.
func (c *Client) ResolveAccountChannels(ctx context.Context, channelOf func(externalID string) string) (resolved, unknown int, err error) {
	var pending []models.ChannelAccount
	if err := c.DB.WithContext(ctx).Where("channel = ''").Find(&pending).Error; err != nil {
		return 0, 0, fmt.Errorf("accounts without a channel: %w", err)
	}
	for _, a := range pending {
		ch := ""
		if channelOf != nil {
			ch = strings.TrimSpace(channelOf(a.ExternalID))
		}
		if ch == "" {
			unknown++
			continue
		}
		if err := c.DB.WithContext(ctx).Model(&models.ChannelAccount{}).
			Where("id = ? AND channel = ''", a.ID).Update("channel", ch).Error; err != nil {
			return resolved, unknown, fmt.Errorf("fill an account's channel: %w", err)
		}
		resolved++
	}
	return resolved, unknown, nil
}

// ensurePerson makes the users row a person (same ID), a member of its household, and, when the
// channel is known, the owner of the account it writes from. Every path that creates a user calls
// it in the same transaction (double write until V4-44 drops users).
func ensurePerson(tx *gorm.DB, u models.User, channelName string) error {
	if u.ID == "" {
		return fmt.Errorf("ensure person: the user has no ID")
	}
	p := models.Person{ID: u.ID, DisplayName: strings.TrimSpace(u.Name), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"display_name": p.DisplayName, "updated_at": p.UpdatedAt})}).
		Create(&p).Error; err != nil {
		return fmt.Errorf("ensure person: %w", err)
	}
	if strings.TrimSpace(u.HouseholdID) != "" {
		m := models.Membership{PersonID: u.ID, HouseholdID: u.HouseholdID, CreatedAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "person_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"household_id": u.HouseholdID})}).
			Create(&m).Error; err != nil {
			return fmt.Errorf("ensure membership: %w", err)
		}
	}
	ext := strings.TrimSpace(u.PlatformID)
	if channelName == "" || ext == "" {
		return nil
	}
	var a models.ChannelAccount
	err := tx.Where("channel = ? AND external_id = ?", channelName, ext).First(&a).Error
	switch {
	case err == nil && a.PersonID != u.ID:
		return fmt.Errorf("ensure person: the %s account already belongs to someone else", channelName)
	case err == nil:
	case errors.Is(err, gorm.ErrRecordNotFound):
		a = models.ChannelAccount{PersonID: u.ID, Channel: channelName, ExternalID: ext, LinkedAt: time.Now()}
		if err := tx.Create(&a).Error; err != nil {
			return fmt.Errorf("ensure channel account: %w", err)
		}
	default:
		return err
	}
	return tx.Exec(`UPDATE people SET preferred_account_id = ? WHERE id = ? AND preferred_account_id IS NULL`, a.ID, u.ID).Error
}
