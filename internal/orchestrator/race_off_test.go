//go:build !race

package orchestrator

// timingSlack scales wall-clock budgets in performance assertions (see race_on_test.go).
const timingSlack = 1
