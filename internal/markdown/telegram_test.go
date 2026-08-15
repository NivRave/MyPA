package markdown

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToTelegramHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bold and Italic",
			input:    "This is **bold** and *italic*.",
			expected: "This is <b>bold</b> and <i>italic</i>.",
		},
		{
			name:     "Escaping HTML",
			input:    "5 < 10 && 10 > 5",
			expected: "5 &lt; 10 &amp;&amp; 10 &gt; 5",
		},
		{
			name:     "List",
			input:    "- Item 1\n- Item 2",
			expected: "• Item 1\n• Item 2",
		},
		{
			name:     "Links",
			input:    "[Google](https://google.com)",
			expected: "<a href=\"https://google.com\">Google</a>",
		},
		{
			name:     "Code block",
			input:    "```go\nfmt.Println(\"Hi\")\n```",
			expected: "<pre><code>fmt.Println(&#34;Hi&#34;)\n</code></pre>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToTelegramHTML(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
