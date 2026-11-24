package shared

import (
	"context"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"strings"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
)

// NotificationSender defines a callback to dispatch partner/family alerts.
type NotificationSender func(ctx context.Context, recipientID, text string) error

// DataProvider defines data access methods required by Notifier to check permissions
// and discover family members. It reads households too: the notifier is an event subscriber, so
// it builds its scope from the event's stored household (V4-30, identity.ForStoredRow).
type DataProvider interface {
	identity.Store
	GetSharedSpace(s identity.Scope, topic string) (*models.SharedSpace, error)
	UsersInHousehold(ctx context.Context, s identity.Scope) ([]models.User, error)
}

// Notifier routes cross-user partner notifications upon mutation events.
type Notifier struct {
	provider DataProvider
	sender   NotificationSender
}

// NewNotifier creates a new cross-user Notifier.
func NewNotifier(provider DataProvider, sender NotificationSender) *Notifier {
	return &Notifier{
		provider: provider,
		sender:   sender,
	}
}

// SetSender updates the NotificationSender callback.
func (n *Notifier) SetSender(sender NotificationSender) {
	n.sender = sender
}

// RegisterWithBus registers the Notifier's HandleMutationEvent method with an EventBus.
func (n *Notifier) RegisterWithBus(bus *events.EventBus) {
	if bus != nil {
		bus.SubscribeAsync(n.HandleMutationEvent)
	}
}

// HandleMutationEvent processes incoming MutationEvents and dispatches alerts to family partners.
func (n *Notifier) HandleMutationEvent(ctx context.Context, event events.MutationEvent) error {
	// If event has no household, no family partner routing applies
	if strings.TrimSpace(event.HouseholdID) == "" {
		return nil
	}

	normTopic := strings.ToLower(strings.TrimSpace(event.Topic))
	if normTopic == "" {
		return nil
	}

	// A row its person did not share (an unshared transaction or calendar event) is theirs alone,
	// in chat and on the dashboard (V4-35, BL-44), so it is not announced to the others either. A
	// delete carries only the before-image, which counts the same way (BL-67).
	if event.Unshared() {
		return nil
	}
	// A notification waits out its recipient's quiet hours (BL-49), unless the row it announces is
	// urgent, as an urgent reminder is (V4-05).
	if event.Urgent() {
		ctx = proactive.Urgent(ctx)
	}

	// 1. Check sharing permissions and notify mode
	notifyMode := models.NotifyModeInstant
	var targetedUserID string

	var scope identity.Scope
	if n.provider != nil {
		s, err := identity.ForStoredRow(ctx, n.provider, event.HouseholdID, event.ActorUserID)
		if err != nil {
			return fmt.Errorf("failed to resolve household of event: %w", err)
		}
		scope = s
		space, err := n.provider.GetSharedSpace(scope, normTopic)
		if err != nil {
			return fmt.Errorf("failed to check shared space for %s/%s: %w", event.HouseholdID, normTopic, err)
		}

		if space != nil {
			// Explicit permission configured
			if !space.CanView {
				// Topic is explicitly private/hidden
				return nil
			}
			notifyMode = space.NotifyMode
			targetedUserID = space.SharedWithUserID
		} else {
			// Fall back to default sharing policy per docs/PLAN.md
			if !models.IsDefaultSharedTopic(normTopic) {
				// Not a default shared topic and no explicit space configured -> silent/private
				return nil
			}
			notifyMode = models.DefaultNotifyModeForTopic(normTopic)
		}
	} else {
		// No provider: default policy check
		if !models.IsDefaultSharedTopic(normTopic) {
			return nil
		}
	}

	// Only instant notifications are pushed immediately
	if notifyMode != models.NotifyModeInstant {
		return nil
	}

	if n.sender == nil || n.provider == nil {
		return nil
	}

	// 2. Discover family members
	members, err := n.provider.UsersInHousehold(ctx, scope)
	if err != nil {
		return fmt.Errorf("failed to fetch family members for household %s: %w", event.HouseholdID, err)
	}

	actorID := strings.TrimSpace(event.ActorUserID)
	var actorName string

	// Find actor's display name
	for _, m := range members {
		if (m.ID != "" && m.ID == actorID) || (m.PlatformID != "" && m.PlatformID == actorID) {
			actorName = m.Name
			break
		}
	}
	if actorName == "" {
		actorName = "Partner"
	}

	// 3. Format message
	alertText := FormatNotification(event, actorName)

	// 4. Send to eligible family members (excluding actor)
	var errs []error
	for _, m := range members {
		// Skip actor
		if (m.ID != "" && m.ID == actorID) || (m.PlatformID != "" && m.PlatformID == actorID) {
			continue
		}

		// If a specific target partner was configured, skip others
		if targetedUserID != "" && targetedUserID != "*" {
			if m.ID != targetedUserID && m.PlatformID != targetedUserID {
				continue
			}
		}

		recipientID := strings.TrimSpace(m.PlatformID)
		if recipientID == "" {
			continue
		}

		if err := n.sender(ctx, recipientID, alertText); err != nil {
			errs = append(errs, fmt.Errorf("failed to notify member %s: %w", recipientID, err))
		}
	}

	return errors.Join(errs...)
}

// FormatNotification formats a human-friendly cross-user alert string.
// Example: "📢 [Shared • Baby] Sarah logged: Feeding 120ml formula"
func FormatNotification(event events.MutationEvent, actorName string) string {
	rawTopic := strings.TrimSpace(event.Topic)
	if rawTopic == "" {
		rawTopic = "General"
	}
	topicTitle := strings.ToUpper(rawTopic[:1]) + strings.ToLower(rawTopic[1:])

	if actorName == "" {
		actorName = "Partner"
	}

	verb := "logged"
	switch strings.ToLower(strings.TrimSpace(event.Action)) {
	case "complete", "completed":
		verb = "completed"
	case "update", "updated":
		verb = "updated"
	case "delete", "deleted":
		verb = "deleted"
	case "create", "created":
		if strings.Contains(strings.ToLower(event.EntityType), "reminder") || strings.Contains(strings.ToLower(event.EntityType), "task") {
			verb = "created task"
		} else {
			verb = "logged"
		}
	default:
		if event.Action != "" {
			verb = event.Action
		}
	}

	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		if event.EntityType != "" {
			summary = fmt.Sprintf("%s (%s)", event.EntityType, event.EntityID)
		} else {
			summary = "new update"
		}
	}

	return fmt.Sprintf("📢 [Shared • %s] %s %s: %s", topicTitle, actorName, verb, summary)
}
