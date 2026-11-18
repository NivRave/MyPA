//go:build race

package orchestrator

// timingSlack scales wall-clock budgets in performance assertions. The race detector
// instruments every memory access (typically 5-10x slower), so absolute budgets that hold
// in a normal run would fail under `go test -race` without indicating a real regression.
const timingSlack = 10
