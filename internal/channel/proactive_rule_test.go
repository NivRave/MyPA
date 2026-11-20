package channel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A rule decides from when the person last wrote (V4-33): open inside the window, closed after
// it, unreachable past the reach and for an account that never wrote.
func TestProactiveRule_At(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	always := ProactiveRule{}
	window := ProactiveRule{Window: 24 * time.Hour}
	sandbox := ProactiveRule{Window: 24 * time.Hour, Reach: 72 * time.Hour}

	cases := []struct {
		name string
		rule ProactiveRule
		last time.Time
		want Openness
	}{
		{"always, wrote long ago", always, now.Add(-90 * 24 * time.Hour), Open},
		{"always, never wrote", always, time.Time{}, Unreachable},
		{"window, inside", window, now.Add(-23 * time.Hour), Open},
		{"window, at the edge", window, now.Add(-24 * time.Hour), Closed},
		{"window, outside", window, now.Add(-30 * 24 * time.Hour), Closed},
		{"sandbox, outside the window", sandbox, now.Add(-48 * time.Hour), Closed},
		{"sandbox, membership lapsed", sandbox, now.Add(-72 * time.Hour), Unreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.rule.At(c.last, now))
		})
	}
}

func TestProactiveRule_Validate(t *testing.T) {
	base := Capabilities{MaxTextLen: 10, Markup: PlainText, Proactive: "always"}
	assert.NoError(t, base.Validate())

	for name, rule := range map[string]ProactiveRule{
		"negative window":     {Window: -time.Hour},
		"negative reach":      {Reach: -time.Hour},
		"window beyond reach": {Window: 48 * time.Hour, Reach: 24 * time.Hour},
		"empty template key":  {Window: time.Hour, Templates: []string{""}},
	} {
		c := base
		c.Rule = rule
		assert.Error(t, c.Validate(), name)
	}
	c := base
	c.Rule = ProactiveRule{Window: 24 * time.Hour, Reach: 72 * time.Hour, Templates: []string{"reminder"}}
	assert.NoError(t, c.Validate())
	assert.True(t, c.Rule.HasTemplate("reminder"))
	assert.False(t, c.Rule.HasTemplate("briefing"))
}
