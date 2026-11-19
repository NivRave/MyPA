package doctor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCheck returns fixed results, or misbehaves on request.
type stubCheck struct {
	name    string
	results []Result
	panics  bool
	hangs   bool
}

func (s stubCheck) Name() string { return s.name }

func (s stubCheck) Run(ctx context.Context, _ Scope) []Result {
	if s.panics {
		panic("boom")
	}
	if s.hangs {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // ignores cancellation for a while
	}
	return s.results
}

func TestRun_KeepsCheckOrderAndCollectsEveryResult(t *testing.T) {
	d := New(
		stubCheck{name: "a", results: []Result{{Check: "A", Status: StatusOK, Detail: "fine"}}},
		stubCheck{name: "b", results: []Result{
			{Check: "B1", Status: StatusFail, Detail: "broken", NextStep: "fix it"},
			{Check: "B2", Status: StatusInfo, Detail: "n/a"},
		}},
		stubCheck{name: "c", results: []Result{{Check: "C", Status: StatusWarn, Detail: "hmm", NextStep: "look"}}},
	)

	report := d.Run(context.Background(), adminScope)

	var names []string
	for _, r := range report.Results {
		names = append(names, r.Check)
	}
	assert.Equal(t, []string{"A", "B1", "B2", "C"}, names)
	assert.False(t, report.Healthy(), "a failed check makes the report unhealthy")
	assert.Equal(t, 1, report.Count(StatusFail))
	assert.Equal(t, 1, report.Count(StatusWarn))
}

func TestHealthy_WarningsAndInfoDoNotCount(t *testing.T) {
	report := Report{Results: []Result{
		{Check: "A", Status: StatusOK},
		{Check: "B", Status: StatusWarn, NextStep: "x"},
		{Check: "C", Status: StatusInfo},
	}}
	assert.True(t, report.Healthy())
	assert.False(t, Report{}.Healthy(), "a report with no checks proves nothing")
}

// A check that crashes or hangs must not take the report down with it: the other checks still
// report, and the broken one shows up as a failure.
func TestRun_ACrashingOrHangingCheckBecomesAFailure(t *testing.T) {
	d := New(
		stubCheck{name: "Crashing check", panics: true},
		stubCheck{name: "Hanging check", hangs: true},
		stubCheck{name: "ok", results: []Result{{Check: "Fine", Status: StatusOK, Detail: "fine"}}},
	)
	d.CheckTimeout = 30 * time.Millisecond

	start := time.Now()
	report := d.Run(context.Background(), adminScope)
	assert.Less(t, time.Since(start), 2*time.Second)

	got := byCheck(report.Results)
	requireProblem(t, got["Crashing check"], StatusFail, "crashed")
	assert.NotContains(t, got["Crashing check"].Detail, "boom", "panic values are for the log, not the chat")
	requireProblem(t, got["Hanging check"], StatusFail, "did not finish")
	assert.Equal(t, StatusOK, got["Fine"].Status)
}

// V4-17: every failure gives a next step. A check that forgets one still gets a generic step.
func TestRun_EveryProblemHasANextStep(t *testing.T) {
	d := New(stubCheck{name: "sloppy", results: []Result{
		{Check: "Sloppy", Status: StatusFail, Detail: "broken"},
		{Check: "Sloppy too", Status: StatusWarn, Detail: "odd"},
	}})
	for _, r := range d.Run(context.Background(), adminScope).Results {
		assert.NotEmpty(t, r.NextStep, r.Check)
	}
}

func TestMarkdown_ProblemsFirstWithNextSteps(t *testing.T) {
	report := Report{Results: []Result{
		{Check: "Database schema", Status: StatusOK, Detail: "Version 4, up to date."},
		{Check: "WhatsApp", Status: StatusInfo, Detail: "Not configured."},
		{Check: "Backups", Status: StatusWarn, Detail: "The newest backup is not encrypted.", NextStep: "Set `BACKUP_AGE_RECIPIENTS`."},
		{Check: "Telegram webhook", Status: StatusFail, Detail: "No webhook is registered.", NextStep: "Set `TELEGRAM_WEBHOOK_URL` and restart the app."},
	}}

	md := report.Markdown()

	assert.True(t, strings.HasPrefix(md, "🩺 **MyPA health check**"))
	assert.Contains(t, md, "1 problem, 1 warning, 1 check passed")
	// Order: failures, warnings, passed, notes.
	fail := strings.Index(md, "❌ **Telegram webhook**")
	warn := strings.Index(md, "⚠️ **Backups**")
	ok := strings.Index(md, "✅ **Database schema**")
	info := strings.Index(md, "ℹ️ **WhatsApp**")
	require.True(t, fail > 0 && warn > 0 && ok > 0 && info > 0, md)
	assert.True(t, fail < warn && warn < ok && ok < info, "problems come first:\n%s", md)
	assert.Contains(t, md, "➡️ Set `TELEGRAM_WEBHOOK_URL` and restart the app.")
	assert.Contains(t, md, "➡️ Set `BACKUP_AGE_RECIPIENTS`.")
}

func TestMarkdown_AllClear(t *testing.T) {
	md := Report{Results: []Result{
		{Check: "A", Status: StatusOK, Detail: "fine"},
		{Check: "B", Status: StatusOK, Detail: "fine"},
	}}.Markdown()
	assert.Contains(t, md, "All 2 checks passed.")
	assert.NotContains(t, md, "➡️")
}

func TestAgo(t *testing.T) {
	cases := map[time.Duration]string{
		20 * time.Second: "less than a minute ago",
		time.Minute:      "1 minute ago",
		12 * time.Minute: "12 minutes ago",
		90 * time.Minute: "1 hour ago",
		7 * time.Hour:    "7 hours ago",
		47 * time.Hour:   "47 hours ago",
		48 * time.Hour:   "2 days ago",
		79 * time.Hour:   "3 days ago",
		-5 * time.Minute: "less than a minute ago",
	}
	for d, want := range cases {
		assert.Equal(t, want, ago(d), d.String())
	}
}
