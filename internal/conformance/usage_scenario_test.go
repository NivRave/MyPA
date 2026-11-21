package conformance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conformanceCaps are the daily caps every scenario runs under (V4-04).
var conformanceCaps = orchestrator.UsageCaps{PersonDaily: 1000, InstallDaily: 100_000}

// fakeUsage is the usage meter: it keeps the rows the engine records and answers the cap check
// with them plus what a scenario says was used earlier today.
type fakeUsage struct {
	mu          sync.Mutex
	rows        []models.ModelUsage
	earlier     map[string]int64 // person ID → tokens used earlier today
	installUsed int64
}

func (f *fakeUsage) RecordModelUsage(_ context.Context, u *models.ModelUsage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, *u)
	return nil
}

func (f *fakeUsage) TokensSince(_ context.Context, personID string, _ time.Time) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	person, install := f.earlier[personID], f.installUsed
	for _, u := range f.rows {
		install += u.InputTokens + u.OutputTokens
		if u.PersonID != nil && *u.PersonID == personID {
			person += u.InputTokens + u.OutputTokens
		}
	}
	for _, n := range f.earlier {
		install += n
	}
	return person, install, nil
}

func (f *fakeUsage) used(personID string, tokens int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.earlier == nil {
		f.earlier = map[string]int64{}
	}
	f.earlier[personID] += tokens
}

// usageCap (V4-04): a member who has used their daily cap gets one clear message, on every
// channel, and the model is never consulted; a command still works, and another member of the
// same household is still served.
func usageCap(t *testing.T, e *env) {
	capped := e.member(1, "family", "Levi")
	other := e.member(2, "family", "Levi")
	u, ok := e.db.user(capped.UserID)
	require.True(t, ok)
	e.usage.used(u.ID, conformanceCaps.PersonDaily)

	e.says(capped, "What is my plan for today?")
	assert.Empty(t, e.model.requests(), "a capped member's message must not reach the model")
	require.Len(t, e.wire.messages(), 1)
	assert.Contains(t, e.lastText(), "allowance of the assistant (1000 of 1000 tokens)") // the wire text is escaped on Telegram
	assert.Contains(t, e.lastText(), "00:00 (UTC)")

	// /undo is one of the commands the message names; it needs no model.
	e.says(capped, "/undo")
	require.Greater(t, len(e.wire.messages()), 1, "a command still works")
	assert.NotContains(t, e.lastText(), "allowance")
	assert.Empty(t, e.model.requests())

	e.model.script(say("Here is your plan."))
	e.says(other, "What is my plan for today?")
	require.Len(t, e.model.requests(), 1, "the cap is the capped member's, not the household's")
	e.requireAllSentTo(capped, capped, other)
	e.golden("usage_cap")
}
