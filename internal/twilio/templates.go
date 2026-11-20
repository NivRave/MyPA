package twilio

// Template keys: what a proactive message names in channel.Proactive.TemplateKey (V4-33).
const (
	TemplateReminder = "reminder"
	TemplateBriefing = "briefing"
)

// Template is one WhatsApp message template MyPA provides for a registered sender (V4-41, ADR 0003
// §5). Each owner submits it in their own Twilio account (`mypa channel-setup whatsapp`), and
// WhatsApp approves it there; MyPA keeps only the resulting Content SID.
type Template struct {
	Key      string // TemplateReminder or TemplateBriefing
	Name     string // the template's name at WhatsApp: lower case, digits and underscores
	Category string // UTILITY: a message the person asked for, not marketing
	Language string // the language code WhatsApp approves it in
	// Body has numbered variables, {{1}} first. WhatsApp refuses a body that starts or ends with a
	// variable, so each sits inside a sentence.
	Body string
	// Samples are example values, one per variable, in order: approval needs them.
	Samples []string
	// Why says when MyPA uses it, for the guided submission.
	Why string
}

// Templates returns MyPA's two templates: a reminder carrying its title as one short line, and a
// note that a briefing is waiting. Everything else goes out inside the 24-hour window, which
// needs no template. English only for now (BL-54).
func Templates() []Template {
	return []Template{
		{
			Key:      TemplateReminder,
			Name:     "mypa_reminder",
			Category: "UTILITY",
			Language: "en",
			Body:     "🔔 Reminder: {{1}}. Reply to this message to see it in full or to change it.",
			Samples:  []string{"Pick up the dry cleaning"},
			Why:      "A reminder that falls due more than 24 hours after the person last wrote.",
		},
		{
			Key:      TemplateBriefing,
			Name:     "mypa_briefing",
			Category: "UTILITY",
			Language: "en",
			Body:     "☀️ Your MyPA briefing for {{1}} is ready. Reply to this message to read it.",
			Samples:  []string{"Monday, 23 November"},
			Why:      "The morning briefing for a person who has not written in the last 24 hours.",
		},
	}
}

// templateByKey returns the template with the given key.
func templateByKey(key string) (Template, bool) {
	for _, t := range Templates() {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}
