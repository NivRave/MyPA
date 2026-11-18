package orchestrator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// BL-9: since P2-9 the history carries earlier turns' tool calls. Asking the model for "a summary of
// all your actions" made it recap those as if just done (a reply listed a calendar read from an
// earlier turn next to the event it had just created). Every summary request must be scoped to the
// user's latest message.
func TestSummaryPrompts_AreScopedToTheLatestMessage(t *testing.T) {
	for name, p := range map[string]string{
		"continuation":   continuationPrompt,
		"tool-limit end": toolLimitSummaryPrompt,
	} {
		t.Run(name, func(t *testing.T) {
			lower := strings.ToLower(p)
			assert.NotContains(t, lower, "all your actions")
			assert.NotContains(t, lower, "so far")
			assert.Contains(t, lower, "latest message")
			assert.Contains(t, lower, "earlier turns")
		})
	}
}

func TestBuildSystemPrompt_ReportsOnlyThisTurnsActions(t *testing.T) {
	p := buildSystemPrompt(promptInputs{})
	assert.Contains(t, p, "only the actions you took for the user's latest message")
	// It belongs to the static, cache-friendly prefix (P2-6), not a per-turn section.
	assert.True(t, strings.HasPrefix(p, staticInstructions))
	assert.Contains(t, staticInstructions, "only the actions you took for the user's latest message")
}
