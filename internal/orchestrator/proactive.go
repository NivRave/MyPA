package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
)

// Delivery is the proactive delivery policy (V4-33). *proactive.Policy implements it. Without one
// (WithDelivery not called) messages the assistant starts go straight to the person's delivery
// account and a refusal is only logged, as before V4-33.
type Delivery interface {
	Deliver(ctx context.Context, req proactive.Request) (*models.ProactiveDelivery, error)
	Release(ctx context.Context, channelName, externalID string)
	Report(ctx context.Context, channelName string, r channel.Report) error
	Sweep(ctx context.Context)
}

// WithDelivery sends every message the assistant starts itself (reminders, briefings and
// notifications) through the delivery policy.
func (e *Engine) WithDelivery(d Delivery) *Engine {
	e.delivery = d
	return e
}

// SweepDeliveries is the policy's minute tick: retries, expiry and purge.
func (e *Engine) SweepDeliveries(ctx context.Context) {
	if e.delivery != nil {
		e.delivery.Sweep(ctx)
	}
}

// deliveredNow reports whether a delivery reached the person in this attempt, so the message
// belongs in their chat history now. One that goes out later (released when the person writes,
// retried, or re-sent after a refusal) is added then, by RecordLateDelivery.
func deliveredNow(d *models.ProactiveDelivery) bool {
	return d != nil && (d.State == models.DeliveryDelivered || d.State == models.DeliveryMoved)
}

// RecordLateDelivery puts a reminder that went out after its first attempt into the person's
// chat history, as a reminder sent at once always was, so the next turn knows about it. The
// policy calls it (proactive.WithLateSend); it runs before the turn that released the message
// reads the history. A briefing's answer is already in the history of the turn that wrote it, and
// notifications never were.
func (e *Engine) RecordLateDelivery(ctx context.Context, d models.ProactiveDelivery) {
	if e.store == nil || d.Category != proactive.Reminder || d.PersonID == "" {
		return
	}
	if err := e.store.AppendChatHistory(ctx, personStateKey(d.PersonID), models.ChatMessage{Role: "assistant", Content: d.Text}); err != nil {
		slog.Warn("late reminder not added to the chat history", "delivery_id", d.ID, "error", err)
	}
}

// reminderTitle is a reminder's text as the one short line a template carries (ADR 0003 §11):
// its first non-empty line. The adapter makes it fit its platform.
func reminderTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(text)
}

// deliverProactive hands one message to the policy, or sends it directly without one. It
// returns the state's history key when the message reached the person now ("" otherwise).
func (e *Engine) deliverProactive(ctx context.Context, key string, req proactive.Request) (historyKey string, err error) {
	if e.delivery == nil {
		to, hk, err := e.deliveryAddress(ctx, key)
		if err != nil {
			return "", err
		}
		return hk, e.sendTo(ctx, to, req.Text)
	}
	req.Key = key
	d, err := e.delivery.Deliver(ctx, req)
	if isNoPerson(err) {
		// A key that names no person has no accounts to choose from: it goes where their latest
		// session was, as before V4-32, and is not recorded.
		to, hk, aerr := e.deliveryAddress(ctx, key)
		if aerr != nil {
			return "", aerr
		}
		return hk, e.sendTo(ctx, to, req.Text)
	}
	if err != nil {
		return "", err
	}
	if !deliveredNow(d) {
		return "", nil
	}
	return personStateKey(d.PersonID), nil
}

// proactiveTurn marks a turn the assistant started itself whose answer is a proactive message
// (the morning briefing): what the answer is delivered as.
type proactiveTurn struct {
	key       string // the person key the turn is for
	category  string
	expiresAt time.Time

	mu             sync.Mutex
	templateKey    string
	templateValues []string
}

type proactiveTurnKey struct{}

func withProactiveTurn(ctx context.Context, t *proactiveTurn) context.Context {
	return context.WithValue(ctx, proactiveTurnKey{}, t)
}

func proactiveTurnOf(ctx context.Context) *proactiveTurn {
	t, _ := ctx.Value(proactiveTurnKey{}).(*proactiveTurn)
	return t
}

// request is what the turn's next answer is delivered as. Only the first may go out as the
// template: one notice is enough for a briefing that took several messages.
func (t *proactiveTurn) request(text string) proactive.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	req := proactive.Request{Category: t.category, Text: text, TemplateKey: t.templateKey,
		TemplateValues: t.templateValues, ExpiresAt: t.expiresAt}
	t.templateKey, t.templateValues = "", nil
	return req
}

// sendProactiveAnswer delivers an answer of a proactive turn through the policy.
func (e *Engine) sendProactiveAnswer(ctx context.Context, t *proactiveTurn, text string) error {
	_, err := e.deliverProactive(ctx, t.key, t.request(text))
	return err
}

// handleReport passes a platform's later refusal to the policy (V4-33). It is not a message:
// nothing else in the turn runs for it.
func (e *Engine) handleReport(ctx context.Context, msg channel.Inbound) error {
	if e.delivery == nil {
		return nil // reported in the adapter's log, as before V4-33
	}
	if err := e.delivery.Report(ctx, msg.Channel, *msg.Report); err != nil {
		slog.Error("delivery report not handled", "channel", msg.Channel, "error", err)
		return err // retried from the queue
	}
	return nil
}

// isNoPerson reports whether a delivery failed because the key names nobody.
func isNoPerson(err error) bool { return errors.Is(err, proactive.ErrNoPerson) }
