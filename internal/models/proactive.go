package models

import "time"

// Proactive delivery states (V4-33, ADR 0003 Decision 3). Pending and held are open; the other
// four are the outcomes a message ends with. A platform's later refusal reopens an outcome.
const (
	// DeliveryPending: to be sent, now or at NextAttemptAt (a send that failed, or a rate limit).
	DeliveryPending = "pending"
	// DeliveryHeld: no route was open; it goes out when the person next writes, or expires.
	DeliveryHeld = "held"
	// DeliveryDelivered: accepted on the person's preferred channel, as text or as a template.
	DeliveryDelivered = "delivered"
	// DeliveryLate: held, then sent when the person wrote, with a note that it is late.
	DeliveryLate = "delivered_late"
	// DeliveryMoved: accepted on another of the person's channels than the preferred one.
	DeliveryMoved = "moved"
	// DeliveryExpired: held until ExpiresAt without the person writing.
	DeliveryExpired = "expired"
	// DeliverySending: claimed by one attempt, so no other sends it at the same time. A row left
	// here by a crash is made pending again after a few minutes.
	DeliverySending = "sending"
)

// Proactive delivery routes: how the message went out, or was last tried.
const (
	RouteDirect   = "direct"   // free text on the preferred channel, inside its window
	RouteTemplate = "template" // an approved template on the preferred channel
	RouteOther    = "other"    // free text on another of the person's channels
	RouteOnReply  = "on_reply" // free text where the person next wrote
)

// ProactiveDelivery is one message the assistant started itself: a reminder, a briefing or a
// notification (V4-33). It is written before the first attempt, so a message is never marked
// sent on hope: its state says where it is, and its outcome is recorded. Text is kept so a held
// message can be sent later and a refused one re-sent; rows are purged 30 days after they end.
type ProactiveDelivery struct {
	ID       string `json:"id" gorm:"primarykey;type:uuid;default:gen_random_uuid()"`
	PersonID string `json:"person_id" gorm:"type:uuid;not null;index"`
	// Category is reminder, briefing or notification (proactive.Reminder and so on).
	Category string `json:"category" gorm:"not null"`
	Text     string `json:"-" gorm:"not null;default:''"`
	// TemplateKey names the template the message may go out as outside a channel's window;
	// TemplateValues is its variables as a JSON array.
	TemplateKey    string `json:"template_key" gorm:"not null;default:''"`
	TemplateValues string `json:"-" gorm:"not null;default:''"`
	State          string `json:"state" gorm:"not null;index"`
	// Route is how it went out (or was last tried); Channel and ExternalID the account.
	Route      string `json:"route" gorm:"not null;default:''"`
	Channel    string `json:"channel" gorm:"not null;default:''"`
	ExternalID string `json:"-" gorm:"not null;default:''"`
	// Refs are the platform's IDs of what was last sent, comma-separated, to match a later report.
	Refs string `json:"-" gorm:"not null;default:''"`
	// Skip lists the routes refused for this message, comma-separated ("<channel>:<account>" for
	// free text, with "#template" for its template, and "!" when the account was not reachable),
	// so a retry does not repeat them.
	Skip string `json:"skip" gorm:"not null;default:''"`
	// Reason is the last refusal or error, without content.
	Reason        string     `json:"reason" gorm:"not null;default:''"`
	Attempts      int        `json:"attempts" gorm:"not null;default:0"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	// DueAt is when the message was due; a late one says so.
	DueAt       time.Time  `json:"due_at" gorm:"not null"`
	ExpiresAt   time.Time  `json:"expires_at" gorm:"not null"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" gorm:"not null"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"not null"`
}

// TableName pins the table name.
func (ProactiveDelivery) TableName() string { return "proactive_deliveries" }
