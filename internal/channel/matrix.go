package channel

import (
	"fmt"
	"strings"
)

// matrixRow is one capability in the generated matrix: how to show an adapter's declared value,
// and the documented fallback when the capability is missing (Render applies the first four).
type matrixRow struct {
	label    string
	value    func(a Adapter) string
	fallback string
}

var matrixRows = []matrixRow{
	{"Receives messages", func(a Adapter) string {
		if a.Ingress().NeedsPublicAddress() {
			return "webhook (needs a public address)"
		}
		return "outward connection (no public address)"
	}, "—"},
	{"Buttons (`MaxChoices`)", func(a Adapter) string {
		c := a.Capabilities()
		if c.MaxChoices == 0 {
			return "none"
		}
		return fmt.Sprintf("up to %d, labels up to %s bytes", c.MaxChoices, thousands(c.MaxChoiceLabel))
	}, "all choices as a numbered list at the end of the text, never a mix; the person types the number or the words"},
	{"Invite link (`InviteLink`)", func(a Adapter) string { return yesOrNo(a.Capabilities().InviteLink) }, "the typed pairing code"},
	{"Group chats (`Groups`)", func(a Adapter) string { return yesOrNo(a.Capabilities().Groups) }, "direct chats only"},
	{"Voice replies (`VoiceOut`)", func(a Adapter) string { return yesOrNo(a.Capabilities().VoiceOut) }, "the text alone"},
	{"Files received (`AttachmentsIn`)", func(a Adapter) string { return kinds(a.Capabilities().AttachmentsIn) },
		"the adapter drops the message: nothing is downloaded or answered"},
	{"Files sent (`AttachmentsOut`)", func(a Adapter) string { return kinds(a.Capabilities().AttachmentsOut) },
		"a line saying the file could not be sent here"},
	{"Longest message (`MaxTextLen`)", func(a Adapter) string { return thousands(a.Capabilities().MaxTextLen) + " bytes" },
		"longer text is split by the adapter"},
	{"Formatting (`Markup`)", func(a Adapter) string { return markupName(a.Capabilities().Markup) }, "—"},
	{"Unprompted messages (`Proactive`)", func(a Adapter) string { return a.Capabilities().Proactive }, "—"},
	{"Before the first message (`JoinStep`)", func(a Adapter) string {
		if j, ok := a.(Joiner); ok && strings.TrimSpace(j.JoinStep()) != "" {
			return j.JoinStep()
		}
		return "nothing"
	}, "—"},
}

// columnName is an adapter's column header: its name, and its mode when it has several (Moded).
func columnName(a Adapter) string {
	if m, ok := a.(Moded); ok && m.Mode() != "" {
		return fmt.Sprintf("`%s` (%s)", a.Name(), m.Mode())
	}
	return fmt.Sprintf("`%s`", a.Name())
}

// Matrix renders the capability matrix of the given adapters as a Markdown table: one column per
// adapter, in order, one row per capability, and the documented fallback for each. It reads the
// declarations and nothing else, and is what SYSTEM.md shows (go run ./cmd/devtools/gendocs).
func Matrix(adapters []Adapter) string {
	if len(adapters) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| Capability |")
	for _, a := range adapters {
		fmt.Fprintf(&b, " %s |", columnName(a))
	}
	b.WriteString(" When missing |\n|")
	for i := 0; i < len(adapters)+2; i++ {
		b.WriteString(" :--- |")
	}
	b.WriteByte('\n')
	for _, row := range matrixRows {
		fmt.Fprintf(&b, "| %s |", row.label)
		for _, a := range adapters {
			fmt.Fprintf(&b, " %s |", cell(row.value(a)))
		}
		fmt.Fprintf(&b, " %s |\n", cell(row.fallback))
	}
	return b.String()
}

func cell(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", `\|`)
}

func yesOrNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func kinds(ks []AttachmentKind) string {
	if len(ks) == 0 {
		return "none"
	}
	names := make([]string, len(ks))
	for i, k := range ks {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

func markupName(m Markup) string {
	switch m {
	case HTML:
		return "HTML"
	case LightMarkup:
		return "light markup (`*bold*`, `_italic_`, triple backticks)"
	case PlainText:
		return "plain text"
	}
	return string(m)
}

// thousands formats n with a comma between groups of three digits.
func thousands(n int) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
