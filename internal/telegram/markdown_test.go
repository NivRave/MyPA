package telegram

import (
	"strings"
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

func TestSafeChunkTelegramHTML_Short(t *testing.T) {
	short := "<b>Hello World</b>"
	chunks := SafeChunkTelegramHTML(short, 100)
	assert.Len(t, chunks, 1)
	assert.Equal(t, short, chunks[0])
}

func TestSafeChunkTelegramHTML_TagBalancing(t *testing.T) {
	// Text with <b> spanning across a 50-char limit
	longBold := "<b>" + strings.Repeat("a", 80) + "</b>"
	chunks := SafeChunkTelegramHTML(longBold, 50)
	assert.True(t, len(chunks) >= 2)
	for i, c := range chunks {
		assert.True(t, len(c) <= 60, "chunk %d len %d", i, len(c))
		assert.True(t, strings.HasPrefix(c, "<b>"), "chunk %d must start with <b>", i)
		assert.True(t, strings.HasSuffix(c, "</b>"), "chunk %d must end with </b>", i)
	}
}

func TestSafeChunkTelegramHTML_NestedCodeBlock(t *testing.T) {
	// Nested <pre><code> block
	code := "<pre><code>" + strings.Repeat("line\n", 30) + "</code></pre>"
	chunks := SafeChunkTelegramHTML(code, 60)
	assert.True(t, len(chunks) >= 2)
	for i, c := range chunks {
		if i == 0 {
			assert.True(t, strings.HasSuffix(c, "</code></pre>"))
		} else {
			assert.True(t, strings.HasPrefix(c, "<pre><code>"))
		}
	}
}

func TestIsTelegramHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"Plain text", "Hello World", false},
		{"Markdown bold", "**Bold text**", false},
		{"Markdown italic", "*Italic text*", false},
		{"Comparison operators", "5 < 10 and 20 > 15", false},
		{"Non-tag angle brackets", "x < y && y > z", false},
		{"Non-Telegram tag", "<script>alert(1)</script>", false},
		{"Placeholder brackets", "<user_id> and <family_group>", false},
		{"Ampersand in text", "Tom & Jerry", false},
		{"HTML entity lt", "5 &lt; 10", true},
		{"HTML entity gt", "20 &gt; 15", true},
		{"HTML entity amp", "Tom &amp; Jerry", true},
		{"HTML entity quot", "He said &quot;hello&quot;", true},
		{"Numeric entity", "Text with &#39;single quote&#39;", true},
		{"HTML tag b", "<b>Bold</b>", true},
		{"HTML tag closing b", "Text ending in </b>", true},
		{"HTML tag i", "<i>Italic</i>", true},
		{"HTML tag code", "<code>code snippet</code>", true},
		{"HTML tag pre", "<pre><code>block</code></pre>", true},
		{"HTML tag link", "<a href=\"https://mypa.app\">MyPA</a>", true},
		{"HTML tag spoiler", "<tg-spoiler>secret</tg-spoiler>", true},
		{"HTML tag blockquote", "<blockquote>quoted text</blockquote>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsTelegramHTML(tt.input))
		})
	}
}
