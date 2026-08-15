package markdown

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToWhatsApp(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
