package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nivik/mypa/internal/graph/model"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
)

// An owner removes a member's channel account from the dashboard (BL-41, ADR 0002 Decision 4): the
// account a stolen, unlocked phone added, when the person has lost every older account. The same
// last-account rule as /unlink holds; the removal is announced like every other account change, to
// the person's remaining accounts and the household's owners, and written to the audit record (an
// audit session, never the mutation log, which holds undoable changes only).

// memberAccounts lists a member's accounts for an owner of the member's household.
func (r *Resolver) memberAccounts(ctx context.Context, hs identity.Scope, personID string) ([]*model.MemberAccount, error) {
	if r.DB == nil {
		return nil, fmt.Errorf("member lookup unavailable")
	}
	accounts, err := r.DB.MemberAccounts(ctx, hs, personID)
	if err != nil {
		return nil, accountRefusal(err)
	}
	out := make([]*model.MemberAccount, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, &model.MemberAccount{
			ID:       a.ID,
			PersonID: a.PersonID,
			Channel:  a.Channel,
			Label:    identity.Describe(a.Channel, a.ExternalID),
			LinkedAt: a.LinkedAt,
		})
	}
	return out, nil
}

// removeMemberAccount removes the account and announces it.
func (r *Resolver) removeMemberAccount(ctx context.Context, hs identity.Scope, personID, accountID string) (bool, error) {
	if r.DB == nil {
		return false, fmt.Errorf("member lookup unavailable")
	}
	removed, remaining, err := r.DB.RemoveMemberAccount(ctx, hs, personID, accountID)
	if err != nil {
		slog.Info("member account removal refused", "household_id", hs.HouseholdID(), "person_id", hs.PersonID(),
			"member_id", personID, "outcome", refusalOutcome(err))
		return false, accountRefusal(err)
	}
	slog.Info("member account removed", "household_id", hs.HouseholdID(), "person_id", hs.PersonID(),
		"member_id", personID, "channel", removed.Channel)
	r.announceAccountRemoval(ctx, hs, personID, *removed, remaining)
	return true, nil
}

// announceAccountRemoval tells the person's remaining accounts and the household's owners. A
// message that cannot be sent is logged; the removal stands.
func (r *Resolver) announceAccountRemoval(ctx context.Context, hs identity.Scope, personID string, removed models.ChannelAccount, remaining []models.ChannelAccount) {
	if r.Announcer == nil {
		return
	}
	text := fmt.Sprintf("🔔 %s was removed from %s's account by %s, on the dashboard.",
		identity.Describe(removed.Channel, removed.ExternalID), r.displayName(ctx, personID, "this person"),
		r.displayName(ctx, hs.PersonID(), "an owner"))
	for _, a := range remaining {
		if err := r.Announcer.SendToAccount(ctx, a, text); err != nil {
			slog.Warn("account removal not announced to the person", "person_id", personID, "channel", a.Channel, "error", err)
		}
	}
	owners, err := r.DB.HouseholdOwners(ctx, hs)
	if err != nil {
		slog.Warn("account removal not announced to the owners", "household_id", hs.HouseholdID(), "error", err)
		return
	}
	for _, o := range owners {
		if o.ID == personID {
			continue // told on their own accounts above
		}
		if err := r.Announcer.SendMessage(ctx, o.PlatformID, text); err != nil {
			slog.Warn("account removal not announced to an owner", "person_id", o.ID, "error", err)
		}
	}
}

// displayName is the person's name, or fallback when they have none.
func (r *Resolver) displayName(ctx context.Context, personID, fallback string) string {
	u, err := r.DB.UserByID(ctx, personID)
	if err != nil || u == nil || strings.TrimSpace(u.Name) == "" || u.Name == "Unknown" {
		return fallback
	}
	return strings.TrimSpace(u.Name)
}

// accountRefusal is the error a refused account change returns to the dashboard.
func accountRefusal(err error) error {
	switch {
	case errors.Is(err, identity.ErrLastAccount):
		return fmt.Errorf("that is the person's last account, so it cannot be removed")
	case errors.Is(err, identity.ErrNotPermitted):
		return fmt.Errorf("permission denied: only an owner of the household may do this")
	case errors.Is(err, identity.ErrNoSuchAccount), errors.Is(err, identity.ErrNoScope):
		return fmt.Errorf("no such account in your household")
	}
	return fmt.Errorf("the account could not be removed; nothing was changed")
}

func refusalOutcome(err error) string {
	switch {
	case errors.Is(err, identity.ErrLastAccount):
		return "refused_last_account"
	case errors.Is(err, identity.ErrNotPermitted):
		return "refused_not_owner"
	case errors.Is(err, identity.ErrNoSuchAccount):
		return "refused_no_such_account"
	}
	return "error"
}
