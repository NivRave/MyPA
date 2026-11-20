package models

import "time"

// Access token purposes (ADR 0002 Decision 5). V4-32 issues link codes only; invites and sign-in
// links arrive with V4-36 and V4-35.
const (
	TokenPurposeInvite = "invite"
	TokenPurposeLink   = "link"
	TokenPurposeSignin = "signin"
)

// Person is a human, separate from the channel accounts they write from (V4-32, ADR 0002
// Decision 1). A person upgraded from a users row keeps that row's ID, so users.id and people.id
// name the same human until V4-44 drops users.
type Person struct {
	ID          string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	DisplayName string `json:"display_name" gorm:"not null;default:''"`
	// IsOperator marks the install's operator (V4-35): backups, restore, /doctor. It gives no
	// access to a household's content. Migration C set it for every admin.
	IsOperator bool `json:"is_operator" gorm:"not null;default:false"`
	// PreferredAccountID is the channel account notifications go to; nil means the account the
	// person last wrote from.
	PreferredAccountID *string   `json:"preferred_account_id,omitempty" gorm:"type:uuid"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TableName pins the table name.
func (Person) TableName() string { return "people" }

// ChannelAccount is one person's identity on one channel: the sender ID an adapter verified.
// One account belongs to one person; a person may have several.
type ChannelAccount struct {
	ID       string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	PersonID string `json:"person_id" gorm:"type:uuid;not null;index"`
	// Channel is the adapter's name (channel.Adapter.Name); ExternalID is channel.Inbound.Sender.
	Channel       string     `json:"channel" gorm:"not null;uniqueIndex:idx_channel_accounts_channel_external,priority:1"`
	ExternalID    string     `json:"external_id" gorm:"not null;uniqueIndex:idx_channel_accounts_channel_external,priority:2"`
	LinkedAt      time.Time  `json:"linked_at" gorm:"not null"`
	LastInboundAt *time.Time `json:"last_inbound_at,omitempty"`
}

// Membership places a person in a household. One per person for now (the unique person_id);
// allowing a second household later is dropping that constraint (ADR 0002 Decision 1, option 1c).
type Membership struct {
	ID          string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	PersonID    string `json:"person_id" gorm:"type:uuid;not null;uniqueIndex"`
	HouseholdID string `json:"household_id" gorm:"type:uuid;not null;index"`
	// Role is owner, adult, child or guest (V4-35, Migration C); identity.ParseRole reads it.
	Role      string    `json:"role" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
}

// AccessToken is a single-use secret: a link code (V4-32), a dashboard sign-in link (V4-35), and
// later an invite (V4-36). Only a hash of the secret is stored; the secret itself is shown once.
type AccessToken struct {
	ID          string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	Purpose     string `json:"purpose" gorm:"not null"`
	TokenHash   string `json:"-" gorm:"not null;uniqueIndex"`
	HouseholdID string `json:"household_id" gorm:"type:uuid;not null;index"`
	// PersonID is whose token it is: for a link code, the person the new account joins.
	PersonID *string `json:"person_id,omitempty" gorm:"type:uuid;index"`
	Role     *string `json:"role,omitempty"`
	// CreatedBy is the person who issued it.
	CreatedBy *string `json:"created_by,omitempty" gorm:"type:uuid"`
	// IssuedOnAccountID is the account a link code was asked for on; its confirmation is asked
	// there and accepted only from there.
	IssuedOnAccountID *string `json:"issued_on_account_id,omitempty" gorm:"type:uuid"`
	// ClaimedByChannel and ClaimedByExternalID are the account that typed a link code; ClaimedAt
	// is when. A claimed code links nothing until the issuing account confirms.
	ClaimedByChannel    *string    `json:"claimed_by_channel,omitempty"`
	ClaimedByExternalID *string    `json:"claimed_by_external_id,omitempty"`
	ClaimedAt           *time.Time `json:"claimed_at,omitempty"`
	// IssuerConfirmedAt is set when the issuing account confirmed a merge with an account that
	// already is a person. The merge itself waits for that account's own confirmation: a merge
	// needs both (ADR 0002 Decision 4).
	IssuerConfirmedAt *time.Time `json:"issuer_confirmed_at,omitempty"`
	ExpiresAt         time.Time  `json:"expires_at" gorm:"not null"`
	UsedAt            *time.Time `json:"used_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// Session is a dashboard sign-in (V4-35, ADR 0002 Decision 5): the browser holds a random secret in
// an HttpOnly cookie, and only its SHA-256 is stored here. It ends after identity.SessionIdle
// without a request, at ExpiresAt (identity.SessionMax after sign-in), or when revoked (/signout,
// signing out in the dashboard). The role is read from the membership on every request, never
// kept here, so a demotion or removal takes effect at once.
type Session struct {
	ID         string     `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	SecretHash string     `json:"-" gorm:"not null;uniqueIndex"`
	PersonID   string     `json:"person_id" gorm:"type:uuid;not null;index"`
	CreatedAt  time.Time  `json:"created_at" gorm:"not null"`
	LastSeenAt time.Time  `json:"last_seen_at" gorm:"not null"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"not null"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}
