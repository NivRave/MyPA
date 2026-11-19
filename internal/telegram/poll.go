package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/logging"
)

// Polling (V4-40): with no public address the adapter fetches updates itself (Bot API getUpdates),
// so a Telegram-only install needs no tunnel. It is the adapter's outward ingress; with a public
// address the webhook is used instead.

const (
	// pollTimeout is how long Telegram holds one getUpdates call open when nothing is waiting. It
	// stays below the client's 30-second HTTP timeout.
	pollTimeout = 25 * time.Second
	// The wait after a failure (Telegram unreachable, the queue down) doubles up to the cap and
	// starts again after a success.
	pollBaseDelay = time.Second
	pollMaxDelay  = time.Minute
)

// updatesAPI is the part of the Bot API polling uses. *Client implements it.
type updatesAPI interface {
	DeleteWebhook(ctx context.Context) error
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]json.RawMessage, error)
}

// poller fetches updates and hands each message to the sink, in order.
type poller struct {
	api                 updatesAPI
	sink                channel.Sink
	timeout             time.Duration
	baseDelay, maxDelay time.Duration
}

func newPoller(api updatesAPI, sink channel.Sink) *poller {
	return &poller{api: api, sink: sink, timeout: pollTimeout, baseDelay: pollBaseDelay, maxDelay: pollMaxDelay}
}

// run polls until ctx ends and returns ctx's error. Nothing stops it before that: failures are
// logged and retried.
//
// Telegram keeps an update until a getUpdates call asks for a higher offset, which confirms every
// update below it. The poller asks for the next offset only after the message was queued, so a
// message that could not be queued is fetched again, as a webhook answered 500 is delivered again.
// A crash between queueing and the next call delivers the message twice; the engine's inbound
// dedup drops the second one, as it does for a repeated webhook.
func (p *poller) run(ctx context.Context) error {
	delay := p.baseDelay
	// Telegram refuses getUpdates while a webhook is set. Removing it keeps the updates waiting.
	for {
		err := p.api.DeleteWebhook(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("telegram deleteWebhook failed; polling starts once it succeeds", "error", err, "retry_in", delay)
		if !sleep(ctx, delay) {
			return ctx.Err()
		}
		delay = p.next(delay)
	}
	slog.Info("telegram polling for updates (no public address)")

	delay = p.baseDelay
	var offset int64
poll:
	for {
		if ctx.Err() != nil {
			return ctx.Err() // a transport that answers after cancellation must not keep the loop going
		}
		updates, err := p.api.GetUpdates(ctx, offset, p.timeout)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, ErrConflict) {
				slog.Error("telegram refused getUpdates: another install is polling this bot or has set a webhook; only one install may be live per bot",
					"retry_in", delay)
			} else {
				slog.Warn("telegram getUpdates failed", "error", err, "retry_in", delay)
			}
			if !sleep(ctx, delay) {
				return ctx.Err()
			}
			delay = p.next(delay)
			continue
		}
		delay = p.baseDelay

		for _, raw := range updates {
			id, err := updateID(raw)
			if err != nil {
				slog.Error("telegram update without an update_id; skipped", "error", err)
				continue
			}
			if id < offset {
				continue // already handled
			}
			msg, err := ParseUpdate(raw)
			switch {
			case err != nil:
				slog.Error("failed to parse telegram update", "error", err)
			case msg != nil:
				// P3-3: never log message content at INFO (length + message_id only).
				logging.LogMessageText(ctx, nil, "received message", msg.Text, "user_id", msg.Sender, "message_id", msg.MessageID)
				if err := p.deliver(ctx, *msg); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					slog.Error("failed to publish message to broker; it is fetched again", "error", err, "retry_in", delay)
					if !sleep(ctx, delay) {
						return ctx.Err()
					}
					delay = p.next(delay)
					continue poll // offset not advanced: Telegram sends this update again
				}
			}
			offset = id + 1
		}
	}
}

// deliver hands one message to the sink with the webhook's deadline.
func (p *poller) deliver(ctx context.Context, msg channel.Inbound) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.sink(ctx, msg)
}

func (p *poller) next(d time.Duration) time.Duration {
	d *= 2
	if d > p.maxDelay {
		d = p.maxDelay
	}
	return d
}

// sleep waits d and reports whether ctx is still alive.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// updateID reads the update_id of a raw update.
func updateID(raw json.RawMessage) (int64, error) {
	var u struct {
		UpdateID *int64 `json:"update_id"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return 0, err
	}
	if u.UpdateID == nil {
		return 0, errors.New("no update_id")
	}
	return *u.UpdateID, nil
}
