// Package doctor implements the `/doctor` health check (ROADMAP V4-17): a set of independent
// checks, each of which reports what it found and, for every problem, what to do next.
//
// The package knows nothing about where its facts come from. Every check takes its inputs as
// small functions, bound to Telegram, Redis, RabbitMQ and Postgres in cmd/mypa, so each
// way an install breaks can be reproduced in a test without any of them running.
package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
)

// Status is the outcome of one check.
type Status int

const (
	// StatusOK: checked and working.
	StatusOK Status = iota
	// StatusInfo: nothing to check (a feature that is not set up) or a fact worth knowing.
	// It never counts against health.
	StatusInfo
	// StatusWarn: works, but something needs attention. The report stays healthy.
	StatusWarn
	// StatusFail: broken. The report is unhealthy.
	StatusFail
)

// Result is one line of the report.
type Result struct {
	Check  string // what was checked, e.g. "Telegram webhook"
	Status Status
	Detail string // what was found; never a secret, a token or a full phone number
	// NextStep says what the admin should do. Every StatusWarn and StatusFail has one.
	NextStep string
}

// Scope says on whose behalf the checks run. Checks about people (Google accounts) are limited
// to the admin's own family group; checks about the install (webhook, queues, backups, schema)
// are the same for every admin.
type Scope struct {
	Admin *models.User
}

// Check is one independent health check. Run returns one or more results and must respect ctx.
type Check interface {
	Name() string
	Run(ctx context.Context, scope Scope) []Result
}

// DefaultCheckTimeout bounds one check. The slowest real check asks Google to refresh one token
// per family member.
const DefaultCheckTimeout = 15 * time.Second

// genericNextStep is used when a check reports a problem without saying what to do.
const genericNextStep = "Check the app log for details, then run /doctor again."

// Doctor runs a fixed list of checks.
type Doctor struct {
	checks []Check
	// CheckTimeout bounds each check; DefaultCheckTimeout when zero.
	CheckTimeout time.Duration
}

// New returns a Doctor that runs the given checks, reporting them in this order.
func New(checks ...Check) *Doctor {
	return &Doctor{checks: checks}
}

// Run executes every check concurrently and returns their results in check order. A check that
// panics or does not finish in time becomes a failed result; it never stops the others.
func (d *Doctor) Run(ctx context.Context, scope Scope) Report {
	timeout := d.CheckTimeout
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}

	outcomes := make([]chan []Result, len(d.checks))
	for i, c := range d.checks {
		done := make(chan []Result, 1) // buffered: a check that finishes late must not block forever
		outcomes[i] = done
		go func(c Check) {
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("doctor check panicked", "check", c.Name(), "panic", r)
					done <- []Result{{
						Check:    c.Name(),
						Status:   StatusFail,
						Detail:   "The check itself crashed.",
						NextStep: genericNextStep,
					}}
				}
			}()
			done <- c.Run(checkCtx, scope)
		}(c)
	}

	deadline := time.NewTimer(timeout + timeout/10)
	defer deadline.Stop()
	timedOut := false

	var report Report
	for i, done := range outcomes {
		var results []Result
		if timedOut {
			select {
			case results = <-done:
			default:
				results = didNotFinish(d.checks[i])
			}
		} else {
			select {
			case results = <-done:
			case <-deadline.C:
				timedOut = true
				results = didNotFinish(d.checks[i])
			case <-ctx.Done():
				timedOut = true
				results = didNotFinish(d.checks[i])
			}
		}
		for _, r := range results {
			if (r.Status == StatusWarn || r.Status == StatusFail) && strings.TrimSpace(r.NextStep) == "" {
				r.NextStep = genericNextStep
			}
			report.Results = append(report.Results, r)
		}
	}
	return report
}

func didNotFinish(c Check) []Result {
	return []Result{{
		Check:    c.Name(),
		Status:   StatusFail,
		Detail:   "The check did not finish in time.",
		NextStep: "Run /doctor again. If it keeps timing out, the service this check talks to is not answering.",
	}}
}

// Report runs the checks for admin and returns the report as Markdown. It satisfies the engine's
// HealthReporter port.
func (d *Doctor) Report(ctx context.Context, admin *models.User) string {
	report := d.Run(ctx, Scope{Admin: admin})
	// Counts only: results can name family members.
	slog.Info("doctor report produced",
		"failed", report.Count(StatusFail), "warnings", report.Count(StatusWarn),
		"passed", report.Count(StatusOK), "healthy", report.Healthy())
	return report.Markdown()
}

// Report is the outcome of one run.
type Report struct {
	Results []Result
}

// Count returns how many results have the given status.
func (r Report) Count(s Status) int {
	n := 0
	for _, res := range r.Results {
		if res.Status == s {
			n++
		}
	}
	return n
}

// Healthy reports whether nothing failed. Warnings and notes do not make an install unhealthy.
// An empty report is not healthy: it checked nothing.
func (r Report) Healthy() bool {
	return len(r.Results) > 0 && r.Count(StatusFail) == 0
}

// Markdown renders the report in the Markdown the engine sends on every channel. Problems come
// first, each followed by its next step; then what passed; then notes.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("🩺 **MyPA health check**\n")
	b.WriteString(r.summary())
	b.WriteString("\n")

	for _, status := range []Status{StatusFail, StatusWarn} {
		for _, res := range r.Results {
			if res.Status != status {
				continue
			}
			fmt.Fprintf(&b, "\n%s **%s**\n%s\n➡️ %s\n", icon(status), res.Check, res.Detail, res.NextStep)
		}
	}
	for _, status := range []Status{StatusOK, StatusInfo} {
		first := true
		for _, res := range r.Results {
			if res.Status != status {
				continue
			}
			if first {
				b.WriteString("\n")
				first = false
			}
			fmt.Fprintf(&b, "%s **%s**: %s\n", icon(status), res.Check, res.Detail)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r Report) summary() string {
	failed, warned, passed := r.Count(StatusFail), r.Count(StatusWarn), r.Count(StatusOK)
	if failed == 0 && warned == 0 {
		return fmt.Sprintf("All %s passed.", plural(passed, "check", "checks"))
	}
	var parts []string
	if failed > 0 {
		parts = append(parts, plural(failed, "problem", "problems"))
	}
	if warned > 0 {
		parts = append(parts, plural(warned, "warning", "warnings"))
	}
	parts = append(parts, plural(passed, "check", "checks")+" passed")
	return strings.Join(parts, ", ") + "."
}

func icon(s Status) string {
	switch s {
	case StatusFail:
		return "❌"
	case StatusWarn:
		return "⚠️"
	case StatusOK:
		return "✅"
	default:
		return "ℹ️"
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ago describes how long ago something happened, in the largest unit that reads naturally.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute", "minutes") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour", "hours") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day", "days") + " ago"
	}
}
