package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func basePromptInputs(t *testing.T) promptInputs {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	return promptInputs{
		UserName:       "Dana",
		Role:           "admin",
		FamilyGroup:    "Levi",
		ActiveTools:    toolSet("create_calendar_event", "save_topic_memory", "search_web"),
		ModuleSections: "### Baby\nLiam is 8 months old.",
		Memories:       []string{"- Allergic to peanuts"},
		Now:            time.Date(2026, 11, 15, 10, 30, 45, 0, time.UTC),
		Location:       loc,
		Calendar:       DefaultCalendarConventions(),
	}
}

func toolSet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// The static instructions are a package-level constant and come first, so every request shares
// the longest possible prefix (Gemini implicit caching).
func TestBuildSystemPrompt_StartsWithStaticInstructions(t *testing.T) {
	p := buildSystemPrompt(basePromptInputs(t))
	assert.True(t, strings.HasPrefix(p, staticInstructions))
	assert.NotContains(t, staticInstructions, "%s", "static block must not be a format template")
}

// Order: static → user → tool guidance → module sections → memories → current time (last).
func TestBuildSystemPrompt_SectionOrder(t *testing.T) {
	p := buildSystemPrompt(basePromptInputs(t))
	idx := func(s string) int {
		i := strings.Index(p, s)
		require.GreaterOrEqual(t, i, 0, "missing %q", s)
		return i
	}
	user := idx("Dana")
	guidance := idx("create_calendar_event")
	modules := idx("Liam is 8 months old")
	memories := idx("Allergic to peanuts")
	clock := idx("Current date and time")
	assert.Less(t, user, guidance)
	assert.Less(t, guidance, modules)
	assert.Less(t, modules, memories)
	assert.Less(t, memories, clock)
	assert.True(t, strings.HasSuffix(strings.TrimSpace(p), strings.TrimSpace(p[clock:])), "time is the final section")
}

// Two turns of the same user a minute apart with different memories share everything up to
// the memories section; only the tail differs.
func TestBuildSystemPrompt_PrefixStableAcrossTurns(t *testing.T) {
	a := basePromptInputs(t)
	b := basePromptInputs(t)
	b.Now = a.Now.Add(3 * time.Minute)
	b.Memories = []string{"- Prefers morning meetings"}

	pa, pb := buildSystemPrompt(a), buildSystemPrompt(b)
	stablePart := pa[:strings.Index(pa, "Allergic to peanuts")]
	assert.GreaterOrEqual(t, commonPrefixLen(pa, pb), len(stablePart)-len("- "), "everything before memories must be identical")
}

// Time is rendered in the user's timezone at minute precision (not server-local, not seconds).
func TestBuildSystemPrompt_TimeInUserZoneMinutePrecision(t *testing.T) {
	p := buildSystemPrompt(basePromptInputs(t))
	assert.Contains(t, p, "2026-11-15 12:30") // 10:30 UTC == 12:30 in Jerusalem (UTC+2 in November)
	assert.Contains(t, p, "Asia/Jerusalem")
	assert.NotContains(t, p, "10:30:45")
	assert.NotContains(t, p, "12:30:45")
}

// Guidance for tools that are not offered this turn must not be in the prompt.
func TestBuildSystemPrompt_ToolGuidanceFollowsActiveTools(t *testing.T) {
	in := basePromptInputs(t)
	p := buildSystemPrompt(in)
	assert.NotContains(t, p, "search_emails", "gmail tools not active")
	assert.NotContains(t, p, "list_task_lists", "tasks tools not active")
	assert.Contains(t, p, "create_calendar_event")

	in.ActiveTools = toolSet("search_emails", "apply_email_labels", "save_topic_memory")
	p = buildSystemPrompt(in)
	assert.Contains(t, p, "search_emails")
	assert.NotContains(t, p, "create_calendar_event")

	// No registry (nil tool set) keeps all guidance.
	in.ActiveTools = nil
	p = buildSystemPrompt(in)
	assert.Contains(t, p, "search_emails")
	assert.Contains(t, p, "create_calendar_event")
}

// Locale conventions come from configuration, not a hard-coded "the user is in Israel".
func TestBuildSystemPrompt_CalendarConventionsFromConfig(t *testing.T) {
	in := basePromptInputs(t)
	p := buildSystemPrompt(in)
	assert.NotContains(t, p, "Israel")
	assert.Contains(t, p, "The week starts on Sunday")
	assert.Contains(t, p, "Friday and Saturday")

	in.Calendar = CalendarConventions{WeekStart: "Monday", Weekend: []string{"Saturday", "Sunday"}}
	p = buildSystemPrompt(in)
	assert.Contains(t, p, "The week starts on Monday")
	assert.Contains(t, p, "Saturday and Sunday")
}

func TestParseCalendarConventions(t *testing.T) {
	c := ParseCalendarConventions("monday", "saturday, sunday")
	assert.Equal(t, "Monday", c.WeekStart)
	assert.Equal(t, []string{"Saturday", "Sunday"}, c.Weekend)

	d := ParseCalendarConventions("", "")
	assert.Equal(t, DefaultCalendarConventions(), d)

	bad := ParseCalendarConventions("funday", "blursday")
	assert.Equal(t, DefaultCalendarConventions(), bad, "invalid values fall back to defaults")
}

// V4-06: the standing instruction to save what the user tells about themselves used to name
// remember_fact. It must survive the removal, naming save_topic_memory, and the removed tool must
// not be mentioned anywhere in the prompt, whichever tools are active.
func TestPrompt_SavingPersonalFactsPointsAtSaveTopicMemory(t *testing.T) {
	in := basePromptInputs(t)

	in.ActiveTools = toolSet("save_topic_memory")
	p := buildSystemPrompt(in)
	assert.Contains(t, p, "use the save_topic_memory tool to save it")
	assert.Contains(t, p, "business card", "the guidance about facts in images is kept")
	assert.NotContains(t, p, "remember_fact")

	in.ActiveTools = nil // no registry: every guidance line is included
	assert.NotContains(t, buildSystemPrompt(in), "remember_fact")

	in.ActiveTools = toolSet("search_web")
	assert.NotContains(t, buildSystemPrompt(in), "save_topic_memory tool to save it", "guidance follows the active tools")
}
