package channel

import (
	"fmt"
	"strings"
)

// Render applies the documented fallback for every part of m the destination cannot show, so the
// adapter receives only what its capabilities declare. It reads the declared values and nothing
// else about the destination. The fallbacks (ADR 0003, Decision 2):
//
//   - Choices, when the channel has no buttons, has fewer than there are choices, or a label is too
//     long: all of them become a numbered list at the end of the text, never a mix of buttons and
//     list. The person replies with the number or the words.
//   - Attachments of a kind the channel cannot send: dropped, with a line saying the file could not
//     be sent here.
//   - Voice, when the channel cannot send it: dropped; the text is the message.
//
// Text length and markup are the adapter's: it converts the text and splits it at MaxTextLen.
func Render(caps Capabilities, m Outbound) Outbound {
	out := m
	var extra []string

	if len(m.Choices) > 0 && !choicesFit(caps, m.Choices) {
		extra = append(extra, ChoiceList(m.Choices))
		out.Choices = nil
	}

	if len(m.Attachments) > 0 {
		out.Attachments = nil
		for _, a := range m.Attachments {
			if Accepts(caps.AttachmentsOut, a.Kind) {
				out.Attachments = append(out.Attachments, a)
				continue
			}
			extra = append(extra, fmt.Sprintf("(The file %q could not be sent here.)", attachmentName(a)))
		}
	}

	if m.Voice != nil && !caps.VoiceOut {
		out.Voice = nil
	}

	if len(extra) > 0 {
		parts := make([]string, 0, len(extra)+1)
		if strings.TrimSpace(out.Text) != "" {
			parts = append(parts, out.Text)
		}
		parts = append(parts, extra...)
		out.Text = strings.Join(parts, "\n\n")
	}
	return out
}

func choicesFit(caps Capabilities, choices []Choice) bool {
	if caps.MaxChoices == 0 || len(choices) > caps.MaxChoices {
		return false
	}
	for _, c := range choices {
		if len(c.Label) > caps.MaxChoiceLabel {
			return false
		}
	}
	return true
}

// ChoiceList renders choices as the numbered list a person answers by typing.
func ChoiceList(choices []Choice) string {
	var b strings.Builder
	for i, c := range choices {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d. %s", i+1, c.Label)
		if typed := strings.TrimSpace(c.TypedReply); typed != "" && typed != c.Label {
			fmt.Fprintf(&b, " (reply %s)", typed)
		}
	}
	return b.String()
}

func attachmentName(a Attachment) string {
	if strings.TrimSpace(a.Name) != "" {
		return a.Name
	}
	if a.ContentType != "" {
		return a.ContentType
	}
	return string(a.Kind)
}
