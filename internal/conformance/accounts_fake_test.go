package conformance

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

// The in-memory side of people and channel accounts (V4-32): the same rules as db.Client's
// identity repository (which is tested against Postgres), reduced to maps.

var _ orchestrator.AccountStore = (*memoryDB)(nil)

// conformanceLinkSecret is the LINK_CODE_SECRET of the suite's install.
const conformanceLinkSecret = "conformance-link-code-secret-0123456789abcdef"

func accountKey(channelName, externalID string) string { return channelName + "|" + externalID }

// addAccount gives the person (users.ID) an account on a channel, linked a while ago.
func (d *memoryDB) addAccount(channelName, externalID, personID string, linkedAt time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addAccountLocked(channelName, externalID, personID, linkedAt)
}

func (d *memoryDB) addAccountLocked(channelName, externalID, personID string, linkedAt time.Time) *models.ChannelAccount {
	a := &models.ChannelAccount{ID: uuid.NewString(), PersonID: personID, Channel: channelName, ExternalID: externalID, LinkedAt: linkedAt}
	d.accounts[accountKey(channelName, externalID)] = a
	if p, ok := d.people[personID]; !ok {
		d.people[personID] = &models.Person{ID: personID, PreferredAccountID: &a.ID}
	} else if p.PreferredAccountID == nil {
		p.PreferredAccountID = &a.ID
	}
	return a
}

func (d *memoryDB) userByIDLocked(id string) (models.User, bool) {
	for _, u := range d.users {
		if u.ID == id {
			return u, true
		}
	}
	return models.User{}, false
}

func (d *memoryDB) UserForAccount(_ context.Context, channelName, externalID string) (*models.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.accounts[accountKey(channelName, externalID)]
	if !ok {
		return nil, errNotFound
	}
	u, ok := d.userByIDLocked(a.PersonID)
	if !ok {
		return nil, errNotFound
	}
	return &u, nil
}

func (d *memoryDB) AccountOf(_ context.Context, channelName, externalID string) (*models.ChannelAccount, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.accounts[accountKey(channelName, externalID)]; ok {
		out := *a
		return &out, nil
	}
	return nil, nil
}

func (d *memoryDB) RecordInbound(_ context.Context, channelName, externalID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.accounts[accountKey(channelName, externalID)]; ok {
		now := time.Now()
		a.LastInboundAt = &now
	}
	return nil
}

func (d *memoryDB) accountsOfLocked(personID string) []models.ChannelAccount {
	var out []models.ChannelAccount
	for _, a := range d.accounts {
		if a.PersonID == personID {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkedAt.Before(out[j].LinkedAt) })
	return out
}

func (d *memoryDB) AccountsOfPerson(_ context.Context, personID string) ([]models.ChannelAccount, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.accountsOfLocked(personID), nil
}

func (d *memoryDB) PersonByID(_ context.Context, id string) (*models.Person, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.people[id]; ok {
		out := *p
		return &out, nil
	}
	return nil, nil
}

// personOfKeyLocked is mypa_person_of: a platform ID or users.id names its user's person.
func (d *memoryDB) personOfKeyLocked(key string) string {
	if u, ok := d.users[key]; ok {
		return u.ID
	}
	if u, ok := d.userByIDLocked(key); ok {
		return u.ID
	}
	return ""
}

func (d *memoryDB) DeliveryAccount(_ context.Context, key string) (*models.ChannelAccount, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	personID := d.personOfKeyLocked(key)
	if personID == "" {
		return nil, nil
	}
	var best *models.ChannelAccount
	preferred := d.people[personID].PreferredAccountID
	for _, a := range d.accountsOfLocked(personID) {
		a := a
		if a.LastInboundAt == nil {
			continue
		}
		if preferred != nil && *preferred == a.ID {
			return &a, nil
		}
		if best == nil || a.LastInboundAt.After(*best.LastInboundAt) {
			best = &a
		}
	}
	return best, nil
}

func (d *memoryDB) SetPreferredAccount(_ context.Context, personID, accountID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.accountsOfLocked(personID) {
		if a.ID == accountID {
			id := accountID
			d.people[personID].PreferredAccountID = &id
			return nil
		}
	}
	return identity.ErrNoSuchAccount
}

func (d *memoryDB) RemoveAccount(_ context.Context, actorAccountID, targetAccountID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var actor, target *models.ChannelAccount
	for _, a := range d.accounts {
		if a.ID == actorAccountID {
			actor = a
		}
		if a.ID == targetAccountID {
			target = a
		}
	}
	switch {
	case actor == nil || target == nil || target.PersonID != actor.PersonID:
		return identity.ErrNoSuchAccount
	case len(d.accountsOfLocked(actor.PersonID)) <= 1:
		return identity.ErrLastAccount
	case target.ID != actor.ID && !actor.LinkedAt.Before(target.LinkedAt):
		return identity.ErrNotOlder
	}
	delete(d.accounts, accountKey(target.Channel, target.ExternalID))
	return nil
}

func (d *memoryDB) IssueLinkCode(_ context.Context, s identity.Scope, issuer models.ChannelAccount, codeHash string) error {
	if !s.Valid() {
		return identity.ErrNoScope
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for _, tok := range d.tokens {
		if tok.PersonID != nil && *tok.PersonID == issuer.PersonID && tok.UsedAt == nil {
			tok.UsedAt = &now
		}
	}
	person, account := issuer.PersonID, issuer.ID
	d.tokens = append(d.tokens, &models.AccessToken{
		ID: uuid.NewString(), Purpose: models.TokenPurposeLink, TokenHash: codeHash, HouseholdID: s.HouseholdID(),
		PersonID: &person, CreatedBy: &person, IssuedOnAccountID: &account,
		ExpiresAt: now.Add(identity.LinkCodeTTL), CreatedAt: now,
	})
	return nil
}

func (d *memoryDB) ClaimLinkCode(_ context.Context, codeHash, channelName, externalID string) (*models.AccessToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for _, tok := range d.tokens {
		if tok.TokenHash == codeHash && tok.ClaimedAt == nil && tok.UsedAt == nil && tok.ExpiresAt.After(now) {
			ch, ext := channelName, externalID
			tok.ClaimedByChannel, tok.ClaimedByExternalID, tok.ClaimedAt = &ch, &ext, &now
			out := *tok
			return &out, nil
		}
	}
	return nil, identity.ErrCodeInvalid
}

func (d *memoryDB) PendingLink(_ context.Context, issuerAccountID string) (*models.AccessToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.tokens) - 1; i >= 0; i-- {
		tok := d.tokens[i]
		if tok.IssuedOnAccountID != nil && *tok.IssuedOnAccountID == issuerAccountID && tok.ClaimedAt != nil &&
			tok.UsedAt == nil && time.Since(*tok.ClaimedAt) < identity.LinkCodeTTL {
			out := *tok
			return &out, nil
		}
	}
	return nil, identity.ErrNothingPending
}

func (d *memoryDB) useLinkCodeLocked(tokenID, issuerAccountID string) (*models.AccessToken, error) {
	for _, tok := range d.tokens {
		if tok.ID == tokenID && tok.IssuedOnAccountID != nil && *tok.IssuedOnAccountID == issuerAccountID &&
			tok.UsedAt == nil && tok.ClaimedAt != nil && time.Since(*tok.ClaimedAt) < identity.LinkCodeTTL {
			now := time.Now()
			tok.UsedAt = &now
			return tok, nil
		}
	}
	return nil, identity.ErrNothingPending
}

func (d *memoryDB) ConfirmLink(_ context.Context, tokenID, issuerAccountID string) (*models.ChannelAccount, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tok, err := d.useLinkCodeLocked(tokenID, issuerAccountID)
	if err != nil {
		return nil, err
	}
	if _, taken := d.accounts[accountKey(*tok.ClaimedByChannel, *tok.ClaimedByExternalID)]; taken {
		return nil, identity.ErrAccountTaken
	}
	a := d.addAccountLocked(*tok.ClaimedByChannel, *tok.ClaimedByExternalID, *tok.PersonID, time.Now())
	a.LastInboundAt = tok.ClaimedAt
	out := *a
	return &out, nil
}

func (d *memoryDB) MergeOnLink(_ context.Context, tokenID, issuerAccountID string) (*identity.MergeResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tok, err := d.useLinkCodeLocked(tokenID, issuerAccountID)
	if err != nil {
		return nil, err
	}
	claimed, ok := d.accounts[accountKey(*tok.ClaimedByChannel, *tok.ClaimedByExternalID)]
	if !ok {
		return nil, identity.ErrNothingPending
	}
	survivor, merged := *tok.PersonID, claimed.PersonID
	su, _ := d.userByIDLocked(survivor)
	mu, _ := d.userByIDLocked(merged)
	if su.HouseholdID != mu.HouseholdID {
		return nil, identity.ErrOtherHousehold
	}
	res := &identity.MergeResult{Survivor: survivor, Merged: merged}
	for _, a := range d.accounts {
		if a.PersonID == merged {
			a.PersonID = survivor
			res.Accounts++
		}
	}
	delete(d.users, mu.PlatformID)
	delete(d.people, merged)
	return res, nil
}
