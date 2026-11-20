package channel

import (
	"errors"
	"fmt"
	"time"
)

// ConversationKind says whether a conversation is a direct chat with one person or a group.
type ConversationKind string

const (
	// Direct is a one-to-one chat between a person and the assistant. An empty kind means Direct.
	Direct ConversationKind = "direct"
	// Group is a chat with several people. An adapter reports it as soon as it can tell; what the
	// assistant does in a group, and when it counts as addressed, is V4-16.
	Group ConversationKind = "group"
)

// Conversation is where a message was written.
type Conversation struct {
	// ID identifies the chat within the channel. It is empty on channels that reply to the person
	// directly rather than to a chat.
	ID string `json:"id,omitempty"`
	// Kind is Direct or Group; empty means Direct.
	Kind ConversationKind `json:"kind,omitempty"`
	// Addressed is true when the message was aimed at the assistant (a mention, a reply to it or a
	// command). Every direct message is addressed.
	Addressed bool `json:"addressed,omitempty"`
}

// AttachmentKind is what an attachment is, as far as the engine needs to know.
type AttachmentKind string

const (
	// KindImage is a photo or picture.
	KindImage AttachmentKind = "image"
	// KindAudio is a voice note or an audio file.
	KindAudio AttachmentKind = "audio"
	// KindDocument is any other file; the engine decides from its content type whether it can read it.
	KindDocument AttachmentKind = "document"
)

// AttachmentRef points at a file a person sent. The adapter fetches it on demand (Adapter.Fetch).
type AttachmentRef struct {
	// ID is the adapter's own reference to the file; only the adapter that issued it can fetch it.
	ID          string         `json:"id"`
	Kind        AttachmentKind `json:"kind"`
	ContentType string         `json:"content_type,omitempty"`
	Size        int64          `json:"size,omitempty"`
	// Label names where the content came from when the engine marks the turn untrusted (P3-1). The
	// adapter sets it. It appears in confirmation prompts, so it is kept stable.
	Label string `json:"label,omitempty"`
}

// Inbound is a message a person sent, in the neutral form every adapter produces. It is the
// payload of the inbound queue.
type Inbound struct {
	// Channel is the name of the adapter that received the message (Adapter.Name).
	Channel string `json:"channel"`
	// MessageID is the platform's ID of the message. It is unique within the conversation (within
	// the sender when Conversation.ID is empty); the duplicate claim uses both.
	MessageID string `json:"message_id"`
	// Sender is the person's ID on this channel, verified by the adapter and never taken from the
	// message text. It names a channel account, which belongs to one person (V4-32).
	Sender       string       `json:"sender"`
	Conversation Conversation `json:"conversation"`
	Text         string       `json:"text,omitempty"`
	// ChoiceID is set when the person pressed a button rather than typing.
	ChoiceID    string          `json:"choice_id,omitempty"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	// ReplyContext is opaque to the engine; it is handed back in Outbound.ReplyContext.
	ReplyContext []byte `json:"reply_context,omitempty"`
	// SentAt is when the platform says the message was sent; zero when it does not say.
	SentAt time.Time `json:"sent_at,omitempty"`
	// Report, when set, makes this not a message but the platform's later report that a message
	// the assistant sent was refused (V4-33). Sender is then the person it was for, MessageID
	// names the report, and Text and Attachments are empty.
	Report *Report `json:"report,omitempty"`
}

// Report is a platform's later answer about a message it had accepted: it was not delivered,
// and why, in the reasons a Receipt uses.
type Report struct {
	// Ref is the platform's ID of the refused message, as Receipt.Refs carried it.
	Ref    string        `json:"ref"`
	Reason RefusalReason `json:"reason"`
}

// ReplyAddress is where a reply to this message goes.
func (in Inbound) ReplyAddress() Address {
	return Address{Channel: in.Channel, Conversation: in.Conversation.ID, Person: in.Sender}
}

// Address is a destination on one channel.
type Address struct {
	// Channel is the adapter's name.
	Channel string
	// Conversation is the chat to send to; empty when the channel sends to the person directly or
	// the chat is not known.
	Conversation string
	// Person is the external ID of the person the message is for.
	Person string
}

// Choice is one option the person can pick. Every choice can also be typed: TypedReply is what
// they type when the channel shows no buttons.
type Choice struct {
	ID         string // at most MaxChoiceIDLen bytes
	Label      string
	TypedReply string
}

// MaxChoiceIDLen is the longest choice ID an Outbound message may carry.
const MaxChoiceIDLen = 64

// Attachment is a file the assistant sends.
type Attachment struct {
	Kind        AttachmentKind
	Name        string
	ContentType string
	Data        []byte
}

// Audio is a voice message the assistant sends.
type Audio struct {
	ContentType string
	Data        []byte
}

// Proactive marks a message the assistant starts itself (a reminder, a briefing, a notification).
// The delivery policy is internal/proactive (V4-33). With TemplateKey set, the adapter sends its
// approved template of that key with TemplateValues instead of Text; the key is one the
// destination lists in ProactiveRule.Templates.
type Proactive struct {
	Category       string
	TemplateKey    string
	TemplateValues []string
}

// Template keys MyPA's proactive messages may name (ADR 0003 §4): a reminder carrying its title
// as one short line, and a note that a briefing is waiting. An adapter that has approved
// templates lists the keys it can send in ProactiveRule.Templates.
const (
	TemplateReminder = "reminder"
	TemplateBriefing = "briefing"
)

// Outbound is a message the assistant sends, in the neutral form. Render applies the fallbacks for
// what the destination cannot show before Adapter.Send is called.
type Outbound struct {
	// Text is in the Markdown subset the model writes; the adapter converts it to its markup and
	// splits it at its length limit.
	Text         string
	Choices      []Choice
	Attachments  []Attachment
	Voice        *Audio
	Proactive    *Proactive // nil for a reply
	ReplyContext []byte
}

// RefusalReason says why a platform did not accept a message.
type RefusalReason string

const (
	// WindowClosed: the platform accepts free-form messages only for a while after the person last
	// wrote.
	WindowClosed RefusalReason = "window_closed"
	// NotReachable: the person cannot be messaged on this channel (unknown, blocked or gone).
	NotReachable RefusalReason = "not_reachable"
	// RateLimited: try again after Receipt.RetryAt.
	RateLimited RefusalReason = "rate_limited"
	// TooLarge: the message or an attachment exceeds what the platform takes.
	TooLarge RefusalReason = "too_large"
)

// Receipt is the platform's answer to a send. Delivery reports that arrive later use the same
// reasons (V4-33).
type Receipt struct {
	Accepted bool
	Reason   RefusalReason // set when not accepted
	RetryAt  time.Time     // set with RateLimited
	// Refs are the platform's IDs of what it accepted (one per part of a split message), so a
	// later Report can be matched to the message. Empty on a platform that sends no reports.
	Refs []string
}

// ProactiveRule is when a channel accepts a message the person did not ask for (V4-33). The
// delivery policy reads it, with when the person last wrote on the channel, before sending.
type ProactiveRule struct {
	// Window, when positive, is how long after the person last wrote the channel accepts a
	// free-form message; zero means always.
	Window time.Duration
	// Reach, when positive, is how long after the person last wrote they can be reached on the
	// channel at all (a sandbox membership lapses); zero means no limit.
	Reach time.Duration
	// Templates are the keys of the approved templates the channel can send outside the window.
	Templates []string
}

// Openness is what a ProactiveRule says about one account at one moment.
type Openness int

const (
	// Open: a free-form message is accepted.
	Open Openness = iota
	// Closed: only a template is accepted.
	Closed
	// Unreachable: nothing reaches the person on this channel.
	Unreachable
)

// At says whether a message to an account that last wrote at lastInbound is accepted at now.
// An account that never wrote (zero lastInbound) is unreachable: platforms refuse a first message
// from the assistant (a bot often cannot start a chat) or have no window to send in.
func (r ProactiveRule) At(lastInbound, now time.Time) Openness {
	if lastInbound.IsZero() {
		return Unreachable
	}
	since := now.Sub(lastInbound)
	switch {
	case r.Reach > 0 && since >= r.Reach:
		return Unreachable
	case r.Window > 0 && since >= r.Window:
		return Closed
	}
	return Open
}

// HasTemplate reports whether the rule lists the template key.
func (r ProactiveRule) HasTemplate(key string) bool {
	for _, k := range r.Templates {
		if k == key {
			return true
		}
	}
	return false
}

// Markup is the text formatting a channel renders.
type Markup string

const (
	// PlainText shows the text as it is.
	PlainText Markup = "plain"
	// HTML is a subset of HTML tags.
	HTML Markup = "html"
	// LightMarkup is single-character emphasis: *bold*, _italic_, ~strike~ and ```code```.
	LightMarkup Markup = "light"
)

// Capabilities is what one configured adapter can do. Render reads these values and nothing else
// about the destination.
type Capabilities struct {
	// MaxChoices is how many buttons one message can carry; 0 means none. MaxChoiceLabel is the
	// longest button label in bytes.
	MaxChoices     int
	MaxChoiceLabel int
	// InviteLink: the channel can open a chat with the assistant from a link that carries a code.
	InviteLink bool
	// Groups: the assistant can take part in group chats.
	Groups bool
	// VoiceOut: the adapter can send a voice message.
	VoiceOut bool
	// AttachmentsIn and AttachmentsOut are the kinds of file the adapter receives and sends.
	AttachmentsIn  []AttachmentKind
	AttachmentsOut []AttachmentKind
	// MaxTextLen is the longest single message in bytes; the adapter splits longer text.
	MaxTextLen int
	Markup     Markup
	// Proactive says, in words, when the channel accepts a message the person did not ask for (for
	// example "always"), for the capability matrix. Rule is the same, for the delivery policy.
	Proactive string
	Rule      ProactiveRule
}

// Validate reports a declaration that cannot be honoured.
func (c Capabilities) Validate() error {
	switch {
	case c.MaxChoices < 0:
		return fmt.Errorf("MaxChoices is negative (%d)", c.MaxChoices)
	case c.MaxChoices > 0 && c.MaxChoiceLabel <= 0:
		return fmt.Errorf("MaxChoices is %d but MaxChoiceLabel is %d", c.MaxChoices, c.MaxChoiceLabel)
	case c.MaxTextLen <= 0:
		return fmt.Errorf("MaxTextLen must be positive (%d)", c.MaxTextLen)
	}
	switch c.Markup {
	case PlainText, HTML, LightMarkup:
	default:
		return fmt.Errorf("unknown Markup %q", c.Markup)
	}
	for _, k := range append(append([]AttachmentKind(nil), c.AttachmentsIn...), c.AttachmentsOut...) {
		switch k {
		case KindImage, KindAudio, KindDocument:
		default:
			return fmt.Errorf("unknown attachment kind %q", k)
		}
	}
	if c.Proactive == "" {
		return errors.New("Proactive must say when unprompted messages are accepted")
	}
	switch {
	case c.Rule.Window < 0 || c.Rule.Reach < 0:
		return errors.New("Rule.Window and Rule.Reach must not be negative")
	case c.Rule.Reach > 0 && c.Rule.Window > c.Rule.Reach:
		return fmt.Errorf("Rule.Window (%s) is longer than Rule.Reach (%s)", c.Rule.Window, c.Rule.Reach)
	}
	for _, k := range c.Rule.Templates {
		if k == "" {
			return errors.New("Rule.Templates has an empty key")
		}
	}
	return nil
}

// Accepts reports whether kind is in kinds.
func Accepts(kinds []AttachmentKind, kind AttachmentKind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}
