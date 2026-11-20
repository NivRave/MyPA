package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Linking channel accounts (V4-32, ADR 0002 Decision 4). The rules live here so the repository
// and the engine refuse the same things for the same reasons.

var (
	// ErrCodeInvalid: the code is unknown, used, expired or already typed by another account.
	ErrCodeInvalid = errors.New("identity: the code is not valid")
	// ErrNothingPending: no typed code is waiting for this account's confirmation.
	ErrNothingPending = errors.New("identity: nothing is waiting for confirmation")
	// ErrAccountTaken: the account that typed the code joined someone while it waited.
	ErrAccountTaken = errors.New("identity: that account already belongs to someone")
	// ErrOtherHousehold: two people of different households cannot be merged (Decision 4).
	ErrOtherHousehold = errors.New("identity: people of different households cannot be merged")
	// ErrLastAccount: a person's only account cannot be removed.
	ErrLastAccount = errors.New("identity: the last account cannot be removed")
	// ErrNotOlder: removing another account needs an older account (or an owner, V4-35).
	ErrNotOlder = errors.New("identity: only an older account can remove this one")
	// ErrNoSuchAccount: the account is not one of the person's.
	ErrNoSuchAccount = errors.New("identity: no such account")
	// ErrAccountTooNew: an account linked less than NewAccountQuarantine ago cannot issue codes.
	ErrAccountTooNew = errors.New("identity: this account was added too recently")
)

// MergeResult says what merging two people moved: the merged person's accounts and the rows
// whose person changed.
type MergeResult struct {
	Survivor string
	Merged   string
	Accounts int64
	Rows     int64
}

// LinkCodeTTL is how long a link code can be typed, and how long a typed code waits for the
// confirmation on the issuing account.
const LinkCodeTTL = 10 * time.Minute

// NewAccountQuarantine is how long a newly added account cannot issue link codes or sign-in links
// or remove older accounts: a stolen, unlocked phone that adds the thief's account gets nothing
// from it before the person notices the announcement (Decision 4).
const NewAccountQuarantine = 24 * time.Hour

// ConfirmLinkReply is the reply that confirms a link. It names its own action: a bare CONFIRM,
// or a confirmation naming a tool, never links an account.
const ConfirmLinkReply = "CONFIRM LINK"

// ConfirmMergeReply is the reply of the account that typed a code, confirming that it merges into
// the issuing person. A merge needs it as well as the issuer's CONFIRM LINK.
const ConfirmMergeReply = "CONFIRM MERGE"

// IsConfirmLink reports whether text is the link confirmation (case and spacing ignored).
func IsConfirmLink(text string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(text), " "), ConfirmLinkReply)
}

// IsConfirmMerge reports whether text is the merge confirmation (case and spacing ignored).
func IsConfirmMerge(text string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(text), " "), ConfirmMergeReply)
}

// MayIssue reports whether an account linked at linkedAt may issue link codes at now.
func MayIssue(linkedAt, now time.Time) bool {
	return !linkedAt.IsZero() && now.Sub(linkedAt) >= NewAccountQuarantine
}

// Describe names an account for a person without showing the whole ID: the channel and the last
// four characters of the external ID ("<channel> ending 0123").
func Describe(channelName, externalID string) string {
	ext := strings.TrimSpace(externalID)
	if n := utf8.RuneCountInString(ext); n > 4 {
		r := []rune(ext)
		ext = string(r[n-4:])
	}
	return channelName + " ending " + ext
}

// codeAlphabet is Crockford's base32: 32 symbols, so an 8-character code carries 40 bits.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// LinkCodeLen is the length of a typed link code.
const LinkCodeLen = 8

// MinCodeKeyLen is the shortest server secret accepted for hashing typed codes.
const MinCodeKeyLen = 32

// NewLinkCode returns a random 8-character code.
func NewLinkCode() (string, error) {
	buf := make([]byte, LinkCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, LinkCodeLen)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)] // 256 is a multiple of 32: no bias
	}
	return string(out), nil
}

// NormalizeCode reads a code as a person may type it: any case, with spaces or dashes, and with
// the letters Crockford's alphabet leaves out read as the digits they look like. It returns ""
// when the result is not a well-formed code.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if !strings.ContainsRune(codeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != LinkCodeLen {
		return ""
	}
	return b.String()
}

// HashCode is the stored form of a typed code: an HMAC-SHA-256 under a server secret that is in
// neither the database nor a backup (ADR 0002 Decision 5), so a leaked database cannot be
// searched for the codes offline. The code is normalized first.
func HashCode(key []byte, code string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(NormalizeCode(code)))
	return hex.EncodeToString(m.Sum(nil))
}
