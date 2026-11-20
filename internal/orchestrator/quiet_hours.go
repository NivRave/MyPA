package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/quiet"
)

// MorningBriefingPrompt is the turn the assistant starts for a person's morning briefing.
const MorningBriefingPrompt = "Good morning! Please generate a brief summary of my schedule for today, summarize my pending TODO tasks, wish me a good day, and remind me of any important upcoming events."

// QuietStore reads one person's time zone and quiet hours (V4-05). db.Client implements it.
// Without one (WithQuietHours not called) nothing is held back and briefings follow the install's
// DEFAULT_TIMEZONE, as before V4-05.
type QuietStore interface {
	QuietSettings(ctx context.Context, userID string) (quiet.Settings, error)
}

// WithQuietHours makes messages the assistant starts itself follow each person's clock: their
// household's time zone and their quiet hours.
func (e *Engine) WithQuietHours(q QuietStore) *Engine {
	e.quietHours = q
	return e
}

// WithClock replaces the engine's clock for the scheduled checks (tests).
func (e *Engine) WithClock(now func() time.Time) *Engine {
	e.now = now
	return e
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// clockOf is the clock of the person behind userID. A failed lookup is logged and treated as no
// quiet hours in the install's zone: a message is then sent as it was before V4-05, never held on
// a guess.
func (e *Engine) clockOf(ctx context.Context, userID string) quiet.Clock {
	if e.quietHours == nil {
		return quiet.NewClock(quiet.Settings{}, e.timezone)
	}
	s, err := e.quietHours.QuietSettings(ctx, userID)
	if err != nil {
		slog.Warn("quiet hours not read; sending as if there were none", "user_id", userID, "error", err)
		return quiet.NewClock(quiet.Settings{}, e.timezone)
	}
	if s.Start != "" && s.End != "" {
		if _, perr := quiet.ParseWindow(s.Start, s.End); perr != nil {
			slog.Warn("quiet hours cannot be read; ignoring them", "user_id", userID, "error", perr)
		}
	}
	return quiet.NewClock(s, e.timezone)
}

// quietClocks caches clockOf for one pass of a scheduled check: one lookup per person.
type quietClocks struct {
	e      *Engine
	byUser map[string]quiet.Clock
}

func (e *Engine) newQuietClocks() *quietClocks {
	return &quietClocks{e: e, byUser: map[string]quiet.Clock{}}
}

func (q *quietClocks) of(ctx context.Context, userID string) quiet.Clock {
	c, ok := q.byUser[userID]
	if !ok {
		c = q.e.clockOf(ctx, userID)
		q.byUser[userID] = c
	}
	return c
}

// holds reports whether a reminder for userID must wait: it is not urgent and now falls in the
// person's quiet hours. A held reminder stays due, so the next pass after the window sends it.
func (q *quietClocks) holds(ctx context.Context, userID string, urgent bool, now time.Time) bool {
	if urgent || q.e.quietHours == nil {
		return false
	}
	return q.of(ctx, userID).QuietAt(now)
}

// SendDueBriefings starts the morning briefing for every person whose briefing minute this is on
// their own clock (quiet.Clock.BriefingDue): 08:00 in their household's time zone, or the end of
// their quiet hours when 08:00 falls inside them (V4-05). The scheduler calls it every minute. It
// returns the people it started a briefing for; Wait blocks until those turns finish.
func (e *Engine) SendDueBriefings(ctx context.Context) []string {
	now := e.clock()
	userIDs, err := e.db.GetUniqueUsers()
	if err != nil {
		slog.Error("failed to get unique users for the morning briefing", "error", err)
		return nil
	}

	var started []string
	for _, userID := range userIDs {
		if !e.clockOf(ctx, userID).BriefingDue(now) {
			continue
		}
		// A system-triggered prompt fed into a turn on behalf of the person, on the account they
		// want notifications on (V4-32).
		to, _, err := e.deliveryAddress(ctx, userID)
		if err != nil {
			slog.Warn("no channel to send the briefing on", "user_id", userID, "error", err)
			continue
		}
		msg := channel.Inbound{
			Channel:      to.Channel,
			MessageID:    fmt.Sprintf("cron-%d", now.Unix()),
			Sender:       to.Person,
			Conversation: channel.Conversation{ID: to.Conversation},
			Text:         MorningBriefingPrompt,
		}
		started = append(started, userID)

		// One goroutine per person, so one slow turn does not delay another.
		e.wg.Add(1)
		go func(m channel.Inbound) {
			defer e.wg.Done()
			bgCtx, cancel := context.WithTimeout(startedByAssistant(context.Background()), 2*time.Minute)
			defer cancel()
			slog.Info("sending the morning briefing", "user_id", m.Sender)
			if err := e.ProcessMessage(bgCtx, m); err != nil {
				slog.Error("failed to process the morning briefing", "user", m.Sender, "error", err)
			}
		}(msg)
	}
	return started
}
