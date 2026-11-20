package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/logging"
	"github.com/nivik/mypa/internal/models"
)

// People and their channel accounts in chat (V4-32, ADR 0002 Decision 4). Linking is a command
// handled before the model runs, never a tool, so text the model reads cannot link an account.
//
//	/link          on a linked account: a code, valid 10 minutes
//	/link CODE     on the new account: asks the first account to confirm
//	CONFIRM LINK   on the first account: links. If the new account already is a person of the same
//	               household, it only asks that account to confirm a merge:
//	CONFIRM MERGE  on the new account: merges it into the first account's person, after a backup
//	/accounts      the person's accounts; /unlink N removes one; /notify N picks where
//	               notifications go
//
// Every one of these works in a direct chat only, so a code is never shown to a group.

// WithAccounts gives the engine people's channel accounts and the server secret typed codes are
// hashed under (LINK_CODE_SECRET). Without a secret, linking is refused; the rest works.
func (e *Engine) WithAccounts(a AccountStore, codeKey []byte) *Engine {
	e.accounts = a
	e.codeKey = append([]byte(nil), codeKey...)
	return e
}

// isDirect reports whether msg was written in a direct chat.
func isDirect(msg channel.Inbound) bool {
	return msg.Conversation.Kind == "" || msg.Conversation.Kind == channel.Direct
}

// linkingReady reports whether linking can run, and replies why not when it cannot.
func (e *Engine) linkingReady(ctx context.Context, msg channel.Inbound) (bool, error) {
	switch {
	case !isDirect(msg):
		return false, e.sendMessage(ctx, msg, "🔒 Accounts are linked in a direct chat with me only, never in a group.")
	case e.accounts == nil || len(e.codeKey) < identity.MinCodeKeyLen:
		return false, e.sendMessage(ctx, msg, "⚠️ Linking accounts is not set up on this install: the operator has to set LINK_CODE_SECRET.")
	}
	return true, nil
}

// linkCodeArg returns the code in "/link CODE" (or "/start link-CODE", what a deep link sends), and
// whether text is a link command with a code at all.
func linkCodeArg(text string) (string, bool) {
	t := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(t, "/link "):
		return strings.TrimSpace(strings.TrimPrefix(t, "/link ")), true
	case strings.HasPrefix(t, "/start link-"):
		return strings.TrimSpace(strings.TrimPrefix(t, "/start link-")), true
	}
	return "", false
}

// withholdCodes keeps a typed link code out of the audit session's prompt (ADR 0002 Decision 5);
// logging.LogMessageText withholds it from the logs the same way.
func withholdCodes(text string) string { return logging.WithholdCodes(text) }

// auditLink records a linking step in the turn's audit session (its tool summary): the purpose,
// the outcome and the token row, never the code.
func auditLink(ctx context.Context, outcome, tokenID string) {
	args := map[string]interface{}{"purpose": "link", "outcome": outcome}
	if tokenID != "" {
		args["token_id"] = tokenID
	}
	recordToolCall(ctx, "account_link", args, outcome)
}

// stateKey is the key of a person's conversational state (chat history, follow-up domains): the
// person (V4-32), so every account of one person continues one direct conversation. A users row
// always has an ID; the bare sender key is for a caller the engine cannot name.
func stateKey(user *models.User, msg channel.Inbound) string {
	if user != nil && strings.TrimSpace(user.ID) != "" {
		return personStateKey(user.ID)
	}
	return msg.Sender
}

// personStateKey is stateKey for a known person.
func personStateKey(personID string) string { return "person:" + personID }

// approvalKey is where a pending approval is kept: the person and the conversation it was raised
// in, so it can be confirmed only there (ADR 0002 §9).
func approvalKey(user *models.User, msg channel.Inbound) string {
	base := stateKey(user, msg)
	if base == msg.Sender {
		return base
	}
	conv := msg.Conversation.ID
	if conv == "" {
		conv = msg.Sender
	}
	return base + "|" + msg.Channel + "|" + conv
}

// deliveryAddress is where a message the assistant starts for the person behind key goes: their
// preferred account, else the one they last wrote from (V4-32). It also returns the person's
// state key, for the chat history. Without an account store, or for a key that names no person,
// it is the channel of their latest audit session, as before V4-32.
func (e *Engine) deliveryAddress(ctx context.Context, key string) (channel.Address, string, error) {
	if e.accounts != nil {
		a, err := e.accounts.DeliveryAccount(ctx, key)
		if err != nil {
			return channel.Address{}, "", err
		}
		if a != nil {
			return channel.Address{Channel: a.Channel, Person: a.ExternalID}, personStateKey(a.PersonID), nil
		}
	}
	if e.db == nil {
		return channel.Address{}, "", fmt.Errorf("no channel found for %s", key)
	}
	log, err := e.db.GetLastAuditSessionForUser(key)
	if err != nil || log == nil || log.Source == "" {
		return channel.Address{}, "", fmt.Errorf("no channel found for %s", key)
	}
	return lastAddress(log, key), key, nil
}

// deliverTo sends text to the person behind key, where they want notifications.
func (e *Engine) deliverTo(ctx context.Context, key, text string) error {
	to, _, err := e.deliveryAddress(ctx, key)
	if err != nil {
		return err
	}
	return e.sendTo(ctx, to, text)
}

// accountAddress is where a message to one specific account goes.
func accountAddress(a models.ChannelAccount) channel.Address {
	return channel.Address{Channel: a.Channel, Person: a.ExternalID}
}

// cmdLinkIssue answers "/link": a new code for this account's person.
func (e *Engine) cmdLinkIssue(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if ok, err := e.linkingReady(ctx, msg); !ok {
		return "", err
	}
	acct, err := e.accounts.AccountOf(ctx, msg.Channel, msg.Sender)
	if err != nil || acct == nil {
		return "", e.sendMessage(ctx, msg, "❌ I could not find this account. Please try again.")
	}
	if !identity.MayIssue(acct.LinkedAt, time.Now()) {
		slog.Info("link code refused", "purpose", "link", "outcome", "account_too_new", "person_id", acct.PersonID, "channel", msg.Channel)
		auditLink(ctx, "refused_account_too_new", "")
		return "", e.sendMessage(ctx, msg, "🔒 This account was added less than 24 hours ago, so it cannot add other accounts yet. Use one of your older accounts.")
	}
	code, err := identity.NewLinkCode()
	if err != nil {
		return "", fmt.Errorf("new link code: %w", err)
	}
	markSideEffect(ctx) // a code was issued (and any older one revoked): never issue twice
	if err := e.accounts.IssueLinkCode(ctx, e.scopeOf(ctx, user), *acct, identity.HashCode(e.codeKey, code)); err != nil {
		slog.Error("link code not issued", "person_id", acct.PersonID, "error", err)
		return "", e.sendMessage(ctx, msg, "❌ I could not make a code. Please try again.")
	}
	slog.Info("link code issued", "purpose", "link", "outcome", "issued", "person_id", acct.PersonID, "household_id", e.scopeOf(ctx, user).HouseholdID(), "channel", msg.Channel)
	auditLink(ctx, "issued", "")
	return "", e.sendMessage(ctx, msg, fmt.Sprintf(
		"🔗 To add another account, send this from it to me within 10 minutes:\n\n/link %s\n\n"+
			"Then confirm here. The code works once. Never send it to anyone: whoever types it asks to join your account.", code))
}

// redeemLinkCode handles "/link CODE" from any account: the code is claimed for this account and
// the issuing account is asked to confirm. user is nil for an account that belongs to nobody.
func (e *Engine) redeemLinkCode(ctx context.Context, user *models.User, msg channel.Inbound, code string) (string, error) {
	if ok, err := e.linkingReady(ctx, msg); !ok {
		return "", err
	}
	// The per-sender lockout holds for every sender, a member's account included.
	if e.store != nil {
		if locked, remaining, lockErr := e.store.CheckPairingLockout(ctx, msg.Sender); lockErr != nil || locked {
			auditLink(ctx, "refused_locked_out", "")
			return "", e.sendMessage(ctx, msg, fmt.Sprintf("🚫 Too many failed attempts. This account is locked out; try again in %d minutes.", int(remaining.Minutes())+1))
		}
	}
	if e.store != nil && e.store.CodesBlocked(ctx) {
		slog.Warn("link code refused", "purpose", "link", "outcome", "install_limit", "channel", msg.Channel)
		auditLink(ctx, "refused_install_limit", "")
		return "", e.sendMessage(ctx, msg, "🚫 Too many wrong codes were typed recently. Please try again in a few minutes.")
	}
	markSideEffect(ctx) // a code is claimed or an attempt counted: a retry must not repeat either
	tok, err := e.accounts.ClaimLinkCode(ctx, identity.HashCode(e.codeKey, code), msg.Channel, msg.Sender)
	if err != nil {
		slog.Info("link code refused", "purpose", "link", "outcome", "invalid", "channel", msg.Channel)
		auditLink(ctx, "refused_invalid", "")
		if e.store != nil {
			_ = e.store.RecordFailedCodeAttempt(ctx)
			attempts, lockedOut, _ := e.store.RecordFailedPairingAttempt(ctx, msg.Sender)
			if lockedOut {
				return "", e.sendMessage(ctx, msg, "🚫 Too many failed attempts (5/5). This account is locked for 1 hour.")
			}
			return "", e.sendMessage(ctx, msg, fmt.Sprintf("🚫 That code is not valid. Ask for a new one with /link on your other account. (%d attempts remaining before a 1-hour lockout)", 5-attempts))
		}
		return "", e.sendMessage(ctx, msg, "🚫 That code is not valid. Ask for a new one with /link on your other account.")
	}
	if e.store != nil {
		_ = e.store.ResetPairingRateLimit(ctx, msg.Sender)
	}

	issuer, err := e.issuingAccount(ctx, tok)
	if err != nil {
		return "", e.sendMessage(ctx, msg, "❌ The account that made this code is gone. Ask for a new one.")
	}
	desc := identity.Describe(msg.Channel, msg.Sender)
	var prompt string
	switch {
	case user == nil:
		prompt = fmt.Sprintf("🔗 Add %s to your account? It will see and change all your data.\n\nReply %s within 10 minutes to add it. If you did not ask for this, ignore this message and nothing changes.",
			desc, identity.ConfirmLinkReply)
	case tok.PersonID != nil && *tok.PersonID == user.ID:
		return "", e.sendMessage(ctx, msg, "ℹ️ This account is already yours.")
	case user.HouseholdID != tok.HouseholdID:
		slog.Info("link code refused", "purpose", "link", "outcome", "other_household", "channel", msg.Channel)
		return "", e.sendMessage(ctx, msg, "🚫 This account belongs to someone in another household, so it cannot be joined to that account.")
	default:
		prompt = fmt.Sprintf("🔗 %s already belongs to %s. Merge the two into one person, with the data of both? A backup is taken first, and %s has to confirm too.\n\nReply %s within 10 minutes. If you did not ask for this, ignore this message and nothing changes.",
			desc, displayName(user), desc, identity.ConfirmLinkReply)
	}
	if err := e.sendTo(ctx, accountAddress(*issuer), prompt); err != nil {
		slog.Error("link confirmation prompt not delivered", "error", err)
		return "", e.sendMessage(ctx, msg, "❌ I could not reach your other account. Please try again.")
	}
	slog.Info("link code claimed", "purpose", "link", "outcome", "claimed", "token_id", tok.ID, "household_id", tok.HouseholdID, "channel", msg.Channel)
	auditLink(ctx, "claimed", tok.ID)
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("⏳ Almost done: confirm on your %s account by replying %s there.", issuer.Channel, identity.ConfirmLinkReply))
}

// issuingAccount is the account a code was asked for on.
func (e *Engine) issuingAccount(ctx context.Context, tok *models.AccessToken) (*models.ChannelAccount, error) {
	if tok.PersonID == nil || tok.IssuedOnAccountID == nil {
		return nil, identity.ErrNoSuchAccount
	}
	accounts, err := e.accounts.AccountsOfPerson(ctx, *tok.PersonID)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].ID == *tok.IssuedOnAccountID {
			return &accounts[i], nil
		}
	}
	return nil, identity.ErrNoSuchAccount
}

// confirmLink handles "CONFIRM LINK" on the issuing account.
func (e *Engine) confirmLink(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if ok, err := e.linkingReady(ctx, msg); !ok {
		return "", err
	}
	acct, err := e.accounts.AccountOf(ctx, msg.Channel, msg.Sender)
	if err != nil || acct == nil {
		return "", e.sendMessage(ctx, msg, "❌ I could not find this account. Please try again.")
	}
	tok, err := e.accounts.PendingLink(ctx, acct.ID)
	if err != nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Nothing is waiting for your confirmation, or it has expired.")
	}
	claimed := models.ChannelAccount{Channel: *tok.ClaimedByChannel, ExternalID: *tok.ClaimedByExternalID}
	desc := identity.Describe(claimed.Channel, claimed.ExternalID)
	existing, err := e.accounts.AccountOf(ctx, claimed.Channel, claimed.ExternalID)
	if err != nil {
		return "", fmt.Errorf("look up the claiming account: %w", err)
	}
	markSideEffect(ctx) // an account is linked or merged: never repeat it

	if existing == nil {
		added, err := e.accounts.ConfirmLink(ctx, tok.ID, acct.ID)
		if err != nil {
			slog.Info("link refused", "purpose", "link", "outcome", linkOutcome(err), "token_id", tok.ID)
			return "", e.sendMessage(ctx, msg, linkRefusal(err))
		}
		slog.Info("account linked", "purpose", "link", "outcome", "used", "token_id", tok.ID, "person_id", added.PersonID, "household_id", tok.HouseholdID, "channel", added.Channel)
		auditLink(ctx, "used", tok.ID)
		_ = e.sendTo(ctx, accountAddress(*added), fmt.Sprintf("✅ Linked. You are %s here too, with the same data. Send /accounts to see your accounts.", displayName(user)))
		e.announceAccountChange(ctx, user, added.PersonID, []string{acct.ID, added.ID}, fmt.Sprintf("🔔 %s was added to %s's account.", desc, displayName(user)))
		return "", e.sendMessage(ctx, msg, fmt.Sprintf("✅ %s is now linked to your account.", desc))
	}

	// The account that typed the code already is a person. A merge needs both sides: this is the
	// issuer's half; the other account must confirm it on its own (ADR 0002 Decision 4: nobody can
	// pull another person's account into theirs by getting them to type a code).
	if existing.PersonID == acct.PersonID {
		return "", e.sendMessage(ctx, msg, "ℹ️ That account is already yours.")
	}
	if _, err := e.accounts.ConfirmMergeByIssuer(ctx, tok.ID, acct.ID); err != nil {
		slog.Info("merge refused", "purpose", "link", "outcome", linkOutcome(err), "token_id", tok.ID)
		return "", e.sendMessage(ctx, msg, linkRefusal(err))
	}
	slog.Info("merge confirmed by the issuer", "purpose", "link", "outcome", "issuer_confirmed", "token_id", tok.ID, "household_id", tok.HouseholdID)
	auditLink(ctx, "issuer_confirmed", tok.ID)
	if err := e.sendTo(ctx, accountAddress(claimed), fmt.Sprintf(
		"⚠️ %s asked to merge this account into theirs. If you confirm, this account and everything it holds become theirs, and you will be %s here.\n\n"+
			"Only do this if both accounts are yours. Reply %s within 10 minutes to merge; otherwise ignore this message and nothing changes.",
		displayName(user), displayName(user), identity.ConfirmMergeReply)); err != nil {
		slog.Error("merge confirmation prompt not delivered", "error", err)
	}
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("⏳ Now confirm on %s by replying %s there.", desc, identity.ConfirmMergeReply))
}

// confirmMerge handles "CONFIRM MERGE" on the account that typed the code, after the issuing
// account confirmed: the second half of a merge. A backup is taken first; if it fails, nothing is
// merged. user is the person being merged; the issuer survives.
func (e *Engine) confirmMerge(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if ok, err := e.linkingReady(ctx, msg); !ok {
		return "", err
	}
	tok, err := e.accounts.PendingMerge(ctx, msg.Channel, msg.Sender)
	if err != nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Nothing is waiting for your confirmation, or it has expired.")
	}
	issuer, err := e.issuingAccount(ctx, tok)
	if err != nil {
		return "", e.sendMessage(ctx, msg, "❌ The account that made this code is gone. Nothing was merged.")
	}
	markSideEffect(ctx) // a backup and a merge: never repeat them
	_ = e.sendMessage(ctx, msg, "🔄 Taking a backup before merging...")
	if path, err := e.backupBeforeMerge(); err != nil {
		slog.Error("merge refused: backup failed", "token_id", tok.ID, "error", err)
		auditLink(ctx, "refused_backup_failed", tok.ID)
		return "", e.sendMessage(ctx, msg, "❌ The backup failed, so nothing was merged. Please try again later.")
	} else {
		slog.Info("backup taken before merging people", "token_id", tok.ID, "path", path)
	}
	res, err := e.accounts.MergeOnLink(ctx, tok.ID, msg.Channel, msg.Sender)
	if err != nil {
		slog.Info("merge refused", "purpose", "link", "outcome", linkOutcome(err), "token_id", tok.ID)
		auditLink(ctx, "refused_"+linkOutcome(err), tok.ID)
		return "", e.sendMessage(ctx, msg, linkRefusal(err))
	}
	slog.Info("people merged", "purpose", "link", "outcome", "used", "token_id", tok.ID, "person_id", res.Survivor, "household_id", tok.HouseholdID, "accounts", res.Accounts, "rows", res.Rows)
	auditLink(ctx, "merged", tok.ID)
	desc := identity.Describe(msg.Channel, msg.Sender)
	_ = e.sendTo(ctx, accountAddress(*issuer), fmt.Sprintf("✅ %s is merged into your account.", desc))
	e.announceAccountChange(ctx, user, res.Survivor, []string{issuer.ID}, fmt.Sprintf("🔔 %s was merged into another member's account.", desc))
	return "", e.sendMessage(ctx, msg, "✅ Merged. This account now belongs to the person who asked, with the data of both.")
}

// backupBeforeMerge takes the backup a merge needs first (ADR 0002 question 4).
func (e *Engine) backupBeforeMerge() (string, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return "", errors.New("DATABASE_URL not set")
	}
	ops := e.backups.withDefaults()
	return ops.backup(databaseURL, ops.dir)
}

func linkOutcome(err error) string {
	switch {
	case errors.Is(err, identity.ErrAccountTaken):
		return "account_taken"
	case errors.Is(err, identity.ErrOtherHousehold):
		return "other_household"
	case errors.Is(err, identity.ErrNothingPending):
		return "expired"
	}
	return "error"
}

func linkRefusal(err error) string {
	switch {
	case errors.Is(err, identity.ErrAccountTaken):
		return "🚫 That account joined someone else meanwhile, so it was not linked."
	case errors.Is(err, identity.ErrOtherHousehold):
		return "🚫 That account belongs to another household, so the two cannot be merged."
	case errors.Is(err, identity.ErrNothingPending):
		return "⚠️ Nothing is waiting for your confirmation, or it has expired."
	}
	return "❌ Something went wrong; nothing was changed. Please try again."
}

// announceAccountChange tells the person's other accounts (all but skip) and the household's
// owners about a change to the person's accounts (Decision 4: every link is announced, so a stolen
// phone adding an account is noticed).
func (e *Engine) announceAccountChange(ctx context.Context, user *models.User, personID string, skip []string, text string) {
	skipped := map[string]bool{}
	for _, id := range skip {
		skipped[id] = true
	}
	if accounts, err := e.accounts.AccountsOfPerson(ctx, personID); err == nil {
		for _, a := range accounts {
			if !skipped[a.ID] {
				_ = e.sendTo(ctx, accountAddress(a), text)
			}
		}
	}
	owners, err := e.db.HouseholdOwners(ctx, e.scopeOf(ctx, user))
	if err != nil {
		return
	}
	for _, m := range owners {
		if m.ID == personID {
			continue
		}
		if err := e.deliverTo(ctx, m.PlatformID, text); err != nil {
			slog.Warn("account change not announced to an owner", "person_id", m.ID, "error", err)
		}
	}
}

// cmdAccounts lists the person's accounts.
func (e *Engine) cmdAccounts(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !isDirect(msg) {
		return "", e.sendMessage(ctx, msg, "🔒 Your accounts are shown in a direct chat with me only.")
	}
	if e.accounts == nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Accounts are not available on this install.")
	}
	accounts, err := e.accounts.AccountsOfPerson(ctx, user.ID)
	if err != nil || len(accounts) == 0 {
		return "", e.sendMessage(ctx, msg, "❌ I could not list your accounts. Please try again.")
	}
	person, _ := e.accounts.PersonByID(ctx, user.ID)
	var sb strings.Builder
	sb.WriteString("👤 *Your accounts*\n\n")
	for i, a := range accounts {
		var notes []string
		if a.Channel == msg.Channel && a.ExternalID == msg.Sender {
			notes = append(notes, "this one")
		}
		if person != nil && person.PreferredAccountID != nil && *person.PreferredAccountID == a.ID {
			notes = append(notes, "notifications go here")
		}
		line := fmt.Sprintf("%d. %s, added %s", i+1, identity.Describe(a.Channel, a.ExternalID), a.LinkedAt.Format("2006-01-02"))
		if len(notes) > 0 {
			line += " (" + strings.Join(notes, ", ") + ")"
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\n/notify N sends notifications to account N. /unlink N removes account N. /link adds another account.")
	return "", e.sendMessage(ctx, msg, sb.String())
}

// accountArg returns the person's account numbered n in /accounts.
func (e *Engine) accountArg(ctx context.Context, user *models.User, arg string) (*models.ChannelAccount, error) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n < 1 {
		return nil, identity.ErrNoSuchAccount
	}
	accounts, err := e.accounts.AccountsOfPerson(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if n > len(accounts) {
		return nil, identity.ErrNoSuchAccount
	}
	return &accounts[n-1], nil
}

// cmdNotify handles "/notify N".
func (e *Engine) cmdNotify(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !isDirect(msg) || e.accounts == nil {
		return "", e.sendMessage(ctx, msg, "🔒 Choose where notifications go in a direct chat with me.")
	}
	target, err := e.accountArg(ctx, user, strings.TrimPrefix(strings.TrimSpace(msg.Text), "/notify"))
	if err != nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Send /accounts to see your accounts, then /notify with the number of one.")
	}
	markSideEffect(ctx)
	if err := e.accounts.SetPreferredAccount(ctx, user.ID, target.ID); err != nil {
		return "", e.sendMessage(ctx, msg, "❌ I could not change that. Please try again.")
	}
	slog.Info("preferred account changed", "person_id", user.ID, "channel", target.Channel)
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("✅ Notifications now go to %s.", identity.Describe(target.Channel, target.ExternalID)))
}

// cmdUnlink handles "/unlink N".
func (e *Engine) cmdUnlink(ctx context.Context, user *models.User, msg channel.Inbound) (string, error) {
	if !isDirect(msg) || e.accounts == nil {
		return "", e.sendMessage(ctx, msg, "🔒 Accounts are removed in a direct chat with me only.")
	}
	actor, err := e.accounts.AccountOf(ctx, msg.Channel, msg.Sender)
	if err != nil || actor == nil {
		return "", e.sendMessage(ctx, msg, "❌ I could not find this account. Please try again.")
	}
	target, err := e.accountArg(ctx, user, strings.TrimPrefix(strings.TrimSpace(msg.Text), "/unlink"))
	if err != nil {
		return "", e.sendMessage(ctx, msg, "⚠️ Send /accounts to see your accounts, then /unlink with the number of one.")
	}
	markSideEffect(ctx)
	if err := e.accounts.RemoveAccount(ctx, actor.ID, target.ID); err != nil {
		slog.Info("account removal refused", "person_id", user.ID, "error", err)
		switch {
		case errors.Is(err, identity.ErrLastAccount):
			return "", e.sendMessage(ctx, msg, "🚫 That is your only account, so it cannot be removed.")
		case errors.Is(err, identity.ErrNotOlder):
			return "", e.sendMessage(ctx, msg, "🚫 Only an account added before that one can remove it.")
		}
		return "", e.sendMessage(ctx, msg, "❌ I could not remove that account. Please try again.")
	}
	desc := identity.Describe(target.Channel, target.ExternalID)
	slog.Info("account removed", "person_id", user.ID, "channel", target.Channel)
	e.announceAccountChange(ctx, user, user.ID, []string{actor.ID}, fmt.Sprintf("🔔 %s was removed from %s's account.", desc, displayName(user)))
	if target.ID == actor.ID {
		return "", e.sendMessage(ctx, msg, "✅ This account is removed. I will not answer it any more.")
	}
	return "", e.sendMessage(ctx, msg, fmt.Sprintf("✅ %s is removed from your account.", desc))
}

func displayName(u *models.User) string {
	if u == nil || strings.TrimSpace(u.Name) == "" || u.Name == "Unknown" {
		return "this person"
	}
	return strings.TrimSpace(u.Name)
}
