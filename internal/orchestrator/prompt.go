package orchestrator

import (
	"fmt"
	"strings"
	"time"
)

// staticInstructions is identical for every user and every turn. It is a constant (not a format
// template) and always comes FIRST so requests share the longest possible prefix — Gemini's
// implicit context caching only reuses a stable prefix. Anything that varies goes after it.
const staticInstructions = "You are an omnipresent personal assistant who helps the user and their family manage their time, tasks, and household. " +
	"If the user has a specific role or family members, keep that context in mind. " +
	"If the user sends an image of a receipt, summarize the expense. " +
	"If the user shares a document, text file (.txt, .md, .csv), or PDF, analyze its contents and fulfill any requests, questions, or workflows requested by the user. " +
	"IMPORTANT: If the user provides a list (e.g., in an image, document, or text), you MUST process ALL items by executing multiple tool calls simultaneously. " +
	"IMPORTANT: If a tool returns JSON or raw data, you MUST summarize and format it into a clean, friendly, conversational response (e.g. using bullet points). NEVER output raw JSON to the user. " +
	"Only call tools that are available to you in this conversation turn."

// toolGuidance is usage guidance that only makes sense when its tools are offered this turn
// (P2-6): guidance about unavailable tools invites the model to call tools it does not have.
// A line is included when ANY tool in `any` is active and ALL tools in `all` are active.
// Order is fixed so the same tool set always yields the same text.
var toolGuidance = []struct {
	any  []string
	all  []string
	text string
}{
	{any: []string{"create_calendar_event"}, text: "If the user asks to schedule a meeting, block time, or create an event, you MUST use the create_calendar_event tool."},
	{any: []string{"update_calendar_event", "delete_calendar_event"}, text: "If the user asks to change or update an event, use update_calendar_event. If they ask to cancel or delete an event, use delete_calendar_event."},
	{any: []string{"list_task_lists", "create_task", "list_tasks"}, text: "If the user asks to manage tasks, to-dos, or lists, use the Google Tasks tools (list_task_lists, create_task_list, list_tasks, create_task, complete_task, delete_task). You can manage multiple task lists."},
	{any: []string{"search_contacts", "create_contact"}, text: "If the user asks to save, find, or retrieve contact information (like an email or phone number), use the search_contacts and create_contact tools."},
	{any: []string{"schedule_workflow"}, text: "If the user asks to schedule a recurring workflow, macro, or cron job (e.g. 'every Monday at 9am'), use the schedule_workflow tool."},
	{any: []string{"search_web"}, text: "If the user asks about recent news, current events, or information you don't know, use the search_web tool to search the internet."},
	{any: []string{"remember_fact"}, text: "If the user tells you a personal fact or preference, use the remember_fact tool to save it for future reference (you can specify scope='personal' or 'family'). If the user sends an image containing a personal fact (e.g. a business card), describe it and use remember_fact to save it."},
	{any: []string{"search_emails"}, text: "If the user asks to check, review, or organize their emails, you MUST DO IT FOR THEM using the search_emails tool. DO NOT create a calendar event to remind them to do it. You can search by 'newer_than:30d', 'is:unread', etc. For each email, optionally use read_email to analyze deeply. Based on content, you may draft_email_reply, archive_emails, or soft_delete_emails. You MUST actively categorize the emails by applying appropriate labels using apply_email_labels (find IDs via list_email_labels). If a new label makes sense, create it using the create_email_label tool. " +
		"GMAIL AUTOMATION GUIDELINES: 1) When asked to organize receipts, use search_emails to find receipts/invoices, create/find a 'Receipts' label, and apply it to all matches using apply_email_labels. 2) When asked to manage subscriptions or clean up newsletters, search for newsletters, read them, and use the unsubscribe_email tool to unsubscribe. 3) When an email asks a question or requires a response, proactively use draft_email_reply to draft a response for the user's review. Summarize all actions taken at the end."},
	{all: []string{"search_emails", "create_calendar_event"}, text: "When an email contains an upcoming meeting, flight, or event ticket, proactively use create_calendar_event to schedule it."},
	{all: []string{"fetch_webpage", "create_task"}, text: "If the user shares a URL and asks you to save it for later, use the fetch_webpage tool to get a summary, and then use the create_task tool to add it to their Google Tasks with the summary in the notes."},
	{any: []string{"create_calendar_event"}, text: "If the user sends an image of a flyer or invitation, extract the details and use create_calendar_event."},
}

// CalendarConventions describes how the household counts weeks (from config, not hard-coded).
type CalendarConventions struct {
	WeekStart string
	Weekend   []string
}

// DefaultCalendarConventions preserves the previous hard-coded behaviour (Israeli week).
func DefaultCalendarConventions() CalendarConventions {
	return CalendarConventions{WeekStart: "Sunday", Weekend: []string{"Friday", "Saturday"}}
}

var weekdayNames = map[string]string{
	"sunday": "Sunday", "monday": "Monday", "tuesday": "Tuesday", "wednesday": "Wednesday",
	"thursday": "Thursday", "friday": "Friday", "saturday": "Saturday",
}

// ParseCalendarConventions parses WEEK_START_DAY / WEEKEND_DAYS ("Friday,Saturday"). Empty or
// invalid values fall back to the defaults (never fail startup over a locale nicety).
func ParseCalendarConventions(weekStart, weekend string) CalendarConventions {
	c := DefaultCalendarConventions()
	if ws, ok := weekdayNames[strings.ToLower(strings.TrimSpace(weekStart))]; ok {
		c.WeekStart = ws
	}
	if strings.TrimSpace(weekend) != "" {
		var days []string
		valid := true
		for _, d := range strings.Split(weekend, ",") {
			name, ok := weekdayNames[strings.ToLower(strings.TrimSpace(d))]
			if !ok {
				valid = false
				break
			}
			days = append(days, name)
		}
		if valid && len(days) > 0 {
			c.Weekend = days
		}
	}
	return c
}

func (c CalendarConventions) sentence() string {
	return fmt.Sprintf("Calendar conventions: The week starts on %s. Weekend days are %s. When reasoning about 'this week' or 'next week', start the week on %s.",
		c.WeekStart, joinAnd(c.Weekend), c.WeekStart)
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return "none"
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// promptInputs is everything that varies between requests, ordered by how often it changes.
type promptInputs struct {
	UserName, Role, FamilyGroup string
	ActiveTools                 map[string]bool // nil = no registry → include all guidance
	ModuleSections              string          // per user, cached 10 min
	Memories                    []string        // per message, pre-formatted "- fact"
	Now                         time.Time
	Location                    *time.Location
	Calendar                    CalendarConventions
}

// buildSystemPrompt assembles the system prompt from most to least stable section:
// static instructions → calendar conventions (per deployment) → user profile (per user) →
// tool guidance (per tool set) → module sections (per user, cached) → memories (per message) →
// current date/time at minute precision (changes every minute) LAST.
func buildSystemPrompt(in promptInputs) string {
	var b strings.Builder
	b.WriteString(staticInstructions)

	b.WriteString("\n\n")
	b.WriteString(in.Calendar.sentence())

	name := in.UserName
	if name == "" {
		name = "the user"
	}
	fmt.Fprintf(&b, "\n\nYou are assisting %s (Role: %s, Family Group: %s).", name, in.Role, in.FamilyGroup)

	var guidance []string
	for _, g := range toolGuidance {
		if guidanceApplies(in.ActiveTools, g.any, g.all) {
			guidance = append(guidance, g.text)
		}
	}
	if len(guidance) > 0 {
		b.WriteString("\n\nTool usage guidance:\n")
		b.WriteString(strings.Join(guidance, "\n"))
	}

	if s := strings.TrimSpace(in.ModuleSections); s != "" {
		b.WriteString("\n\n")
		b.WriteString(s)
	}

	if len(in.Memories) > 0 {
		b.WriteString("\n\nRelevant facts about the user from past conversations:\n")
		b.WriteString(strings.Join(in.Memories, "\n"))
	}

	loc := in.Location
	if loc == nil {
		loc = time.UTC
	}
	fmt.Fprintf(&b, "\n\nCurrent date and time: %s (%s), timezone %s. Assume this timezone for relative dates.",
		in.Now.In(loc).Format("2006-01-02 15:04"), in.Now.In(loc).Format("Monday"), loc.String())
	return b.String()
}

func guidanceApplies(active map[string]bool, anyOf, allOf []string) bool {
	if active == nil {
		return true
	}
	if len(anyOf) > 0 {
		found := false
		for _, t := range anyOf {
			if active[t] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, t := range allOf {
		if !active[t] {
			return false
		}
	}
	return true
}
