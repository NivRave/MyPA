package twilio

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToWhatsApp(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bold and Italic",
			input:    "This is **bold** and *italic*.",
			expected: "This is *bold* and _italic_.",
		},
		{
			name:     "No Escaping HTML",
			input:    "5 < 10 && 10 > 5",
			expected: "5 < 10 && 10 > 5",
		},
		{
			name:     "List",
			input:    "- Item 1\n- Item 2",
			expected: "- Item 1\n- Item 2",
		},
		{
			name:     "Links",
			input:    "[Google](https://google.com)",
			expected: "Google (https://google.com)",
		},
		{
			name:     "Code block",
			input:    "```go\nfmt.Println(\"Hi\")\n```",
			expected: "```\nfmt.Println(\"Hi\")\n```",
		},
		// BL-10: WhatsApp cannot nest identical markers, so "*a *b**" shows literal asterisks.
		// A heading is already bold; bold inside it (or inside bold) must not add markers.
		{
			name:     "Heading containing bold",
			input:    "### 📅 **Event Details**",
			expected: "*📅 Event Details*",
		},
		{
			name:     "Bold nested in bold",
			input:    "**Total: **42** items**",
			expected: "*Total: 42 items*",
		},
		{
			name:     "Heading keeps italic (bold-italic is valid)",
			input:    "## Plan for *today*",
			expected: "*Plan for _today_*",
		},
		{
			name:     "Bold after heading still works",
			input:    "# Title\n\nSome **bold** text.",
			expected: "*Title*\n\nSome *bold* text.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToWhatsApp(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
