package channel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modedAdapter runs in one of several modes and may need a join step (V4-41).
type modedAdapter struct {
	*fakeAdapter
	mode       string
	step       string
	configured bool
}

func (m modedAdapter) Mode() string     { return m.mode }
func (m modedAdapter) JoinStep() string { return m.step }
func (m modedAdapter) Configured() bool { return m.configured }

func TestMatrix_AColumnPerModeAndTheFirstMessage(t *testing.T) {
	sandbox := modedAdapter{fakeAdapter: newFake("chat"), mode: "trial", step: "send `join x` to the trial number first"}
	own := modedAdapter{fakeAdapter: newFake("chat"), mode: "own"}
	plain := newFake("plain")

	got := Matrix([]Adapter{plain, sandbox, own})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	assert.Equal(t, "| Capability | `plain` | `chat` (trial) | `chat` (own) | When missing |", lines[0])
	var first string
	for _, l := range lines {
		if strings.HasPrefix(l, "| Before the first message") {
			first = l
		}
	}
	require.NotEmpty(t, first, "the matrix says what a person does before their first message")
	assert.Contains(t, first, "| nothing | send `join x` to the trial number first | nothing |")
}

func TestRegistry_JoinStepsOfConfiguredAdapters(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(modedAdapter{fakeAdapter: newFake("trial"), mode: "trial", step: "join first", configured: true}))
	require.NoError(t, reg.Register(modedAdapter{fakeAdapter: newFake("off"), step: "join first"})) // not configured
	require.NoError(t, reg.Register(modedAdapter{fakeAdapter: newFake("own"), configured: true}))   // no step
	require.NoError(t, reg.Register(newFake("plain")))                                              // not a Joiner

	assert.Equal(t, []JoinStep{{Channel: "trial", Step: "join first"}}, reg.JoinSteps(),
		"only a configured adapter with a step of its own")
	assert.Nil(t, (*Registry)(nil).JoinSteps())
}
