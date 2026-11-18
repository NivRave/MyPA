package markdown

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyHTMLTagBalancing validates that every opened tag in chunk is closed before chunk ends,
// and returns the sequence of tags that were open at the end (before synthetic closing).
func verifyHTMLTagBalancing(t *testing.T, chunk string) {
	var stack []string
	i := 0
	for i < len(chunk) {
		if chunk[i] == '<' {
			loc := htmlTagRegex.FindStringSubmatchIndex(chunk[i:])
			if len(loc) >= 4 {
				tagStr := chunk[i : i+loc[1]]
				tagName := strings.ToLower(chunk[i+loc[2] : i+loc[3]])
				isClosing := strings.HasPrefix(tagStr, "</")

				if isClosing {
					require.NotEmpty(t, stack, "Closing tag </%s> found with empty stack in chunk: %s", tagName, chunk)
					top := stack[len(stack)-1]
					assert.Equal(t, top, tagName, "Mismatched closing tag </%s>, expected </%s> in chunk: %s", tagName, top, chunk)
					stack = stack[:len(stack)-1]
				} else {
					stack = append(stack, tagName)
				}
				i += loc[1]
				continue
			}
		}
		i++
	}
	assert.Empty(t, stack, "Chunk has unclosed tags at the end: %v in chunk: %s", stack, chunk)
}

// TestSafeChunkTelegramHTML_DeepNestedTags verifies that deeply nested tags
// (<pre><code><b><i>...</i></b></code></pre>) spanning 4000-char chunk boundaries remain
// syntactically balanced across all chunks.
func TestSafeChunkTelegramHTML_DeepNestedTags(t *testing.T) {
	// Construct 10,000 characters of deeply nested HTML
	var innerContent strings.Builder
	for innerContent.Len() < 10000 {
		innerContent.WriteString("Paragraph text with <i>nested italic details</i> and further explanations.\n\n")
	}

	deepHTML := "<pre><code><b>" + innerContent.String() + "</b></code></pre>"
	chunks := SafeChunkTelegramHTML(deepHTML, 4000)

	require.GreaterOrEqual(t, len(chunks), 3, "Expected at least 3 chunks for 10KB+ input")

	for i, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4096, "Chunk %d exceeds Telegram 4096 limit", i)
		assert.True(t, utf8.ValidString(chunk), "Chunk %d must be valid UTF-8", i)

		// Verify every chunk is perfectly balanced
		verifyHTMLTagBalancing(t, chunk)

		if i > 0 {
			// Must reopen open tags from previous chunk
			assert.True(t, strings.HasPrefix(chunk, "<pre><code><b>") || strings.HasPrefix(chunk, "<pre><code>"),
				"Chunk %d should reopen active nested tags: prefix is %q", i, chunk[:min(30, len(chunk))])
		}
	}
}

// TestSafeChunkTelegramHTML_LinksAcrossChunkBoundaries verifies that anchor tags
// (<a href="...">) with query params spanning chunk boundaries are properly closed and reopened.
func TestSafeChunkTelegramHTML_LinksAcrossChunkBoundaries(t *testing.T) {
	linkURL := "https://example.com/oauth/callback?client_id=12345&redirect_uri=https%3A%2F%2Fmypa.app&state=secret_token_abcdef"
	linkOpen := fmt.Sprintf("<a href=\"%s\">", linkURL)

	var sb strings.Builder
	for sb.Len() < 9000 {
		sb.WriteString("Leading text before the massive link content. ")
		sb.WriteString(linkOpen)
		sb.WriteString(strings.Repeat("Click here to view extended partner sync activity and calendar entries. ", 50))
		sb.WriteString("</a>\n\n")
	}

	rawHTML := sb.String()
	chunks := SafeChunkTelegramHTML(rawHTML, 4000)

	require.GreaterOrEqual(t, len(chunks), 3)

	for i, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4096)
		assert.True(t, utf8.ValidString(chunk))
		verifyHTMLTagBalancing(t, chunk)

		// If a chunk contains an open <a href="...">, it must close with </a>
		openACount := strings.Count(chunk, "<a ")
		closeACount := strings.Count(chunk, "</a>")
		assert.Equal(t, openACount, closeACount, "Chunk %d has unequal <a and </a count", i)
	}
}

// TestSafeChunkTelegramHTML_MultibyteUTF8_AdversarialBoundary stresses
// multibyte characters (Hebrew 2-byte, CJK 3-byte, Emoji 4-byte) across chunk boundaries.
func TestSafeChunkTelegramHTML_MultibyteUTF8_AdversarialBoundary(t *testing.T) {
	testCases := []struct {
		name     string
		char     string
		byteLen  int
		maxLen   int
		totalLen int
	}{
		{
			name:     "Hebrew 2-byte characters at 4000-boundary",
			char:     "ש", // 2 bytes: 0xD7 0xA9
			byteLen:  2,
			maxLen:   4000,
			totalLen: 10000,
		},
		{
			name:     "CJK 3-byte characters at 4000-boundary",
			char:     "你", // 3 bytes: 0xE4 0xBD 0xA0
			byteLen:  3,
			maxLen:   4000,
			totalLen: 12000,
		},
		{
			name:     "Emoji 4-byte characters at 4000-boundary",
			char:     "🚀", // 4 bytes: 0xF0 0x9F 0x99 0x82
			byteLen:  4,
			maxLen:   4000,
			totalLen: 12000,
		},
		{
			name:     "Emoji 4-byte characters with small 50-byte chunks",
			char:     "🎉",
			byteLen:  4,
			maxLen:   50,
			totalLen: 500,
		},
		{
			name:     "Hebrew with HTML formatting across 4000 boundary",
			char:     "בדיקה של הודעה ארוכה בעברית עם תוכן חשוב ",
			byteLen:  len("בדיקה של הודעה ארוכה בעברית עם תוכן חשוב "),
			maxLen:   4000,
			totalLen: 10000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			count := tc.totalLen / len(tc.char)
			repeated := strings.Repeat(tc.char, count)
			htmlInput := "<b>" + repeated + "</b>"

			chunks := SafeChunkTelegramHTML(htmlInput, tc.maxLen)
			require.NotEmpty(t, chunks)

			for i, chunk := range chunks {
				// 1. Must be valid UTF-8 (no severed rune bytes)
				isValidUTF8 := utf8.ValidString(chunk)
				assert.True(t, isValidUTF8, "Chunk %d contains invalid UTF-8 bytes! Severed multibyte rune detected.", i)

				// 2. Must be syntactically balanced
				verifyHTMLTagBalancing(t, chunk)

				// 3. Length must be <= maxLen (with small tolerance for closing/reopening tags)
				assert.LessOrEqual(t, len(chunk), tc.maxLen+50, "Chunk %d exceeded budget", i)
			}
		})
	}
}

// TestNoDoubleEscaping verifies that text containing < and > is properly escaped once
// and NOT double-escaped into &amp;lt; and &amp;gt;.
func TestNoDoubleEscaping(t *testing.T) {
	inputs := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Comparison operators",
			input:    "Condition: 5 < 10 and 20 > 15",
			expected: "Condition: 5 &lt; 10 and 20 &gt; 15",
		},
		{
			name:     "HTML-like text",
			input:    "Check <user_id> and <family_group> values",
			expected: "Check &lt;user_id&gt; and &lt;family_group&gt; values",
		},
		{
			name:     "Pre-existing ampersand with brackets",
			input:    "Formula: A & B where x < 5",
			expected: "Formula: A &amp; B where x &lt; 5",
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			out := ToTelegramHTML(tc.input)
			assert.Equal(t, tc.expected, out)
			assert.NotContains(t, out, "&amp;lt;", "Output must not contain double-escaped &amp;lt;")
			assert.NotContains(t, out, "&amp;gt;", "Output must not contain double-escaped &amp;gt;")
		})
	}
}

// TestSafeChunkTelegramHTML_UnclosedTags verifies that unclosed tags
// (<b><i><u>... without matching closing tags) are automatically closed before chunk ends
// and reopened in subsequent chunks, producing valid balanced HTML in each chunk.
func TestSafeChunkTelegramHTML_UnclosedTags(t *testing.T) {
	unclosedHTML := "<b><i><u>" + strings.Repeat("This text has unclosed formatting tags. ", 200)
	chunks := SafeChunkTelegramHTML(unclosedHTML, 4000)

	require.GreaterOrEqual(t, len(chunks), 2)
	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4096)
		verifyHTMLTagBalancing(t, chunk)
	}
}

// TestSafeChunkTelegramHTML_TagsSplitAcrossBoundary places tags directly
// across the 4000-character boundary and verifies that chunking never splits inside a tag.
func TestSafeChunkTelegramHTML_TagsSplitAcrossBoundary(t *testing.T) {
	// Pad prefix to exactly 3995 characters
	prefix := strings.Repeat("A", 3995)
	tag := "<b>Bold Content Across Boundary</b>"
	suffix := strings.Repeat("B", 1000)

	input := prefix + tag + suffix
	chunks := SafeChunkTelegramHTML(input, 4000)

	require.GreaterOrEqual(t, len(chunks), 2)
	for i, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4096)
		assert.False(t, strings.HasPrefix(chunk, ">"), "Chunk %d starts with dangling tag close >", i)
		assert.False(t, strings.HasSuffix(chunk, "<"), "Chunk %d ends with dangling tag open <", i)
		verifyHTMLTagBalancing(t, chunk)
	}
}

// TestSafeChunkTelegramHTML_HTMLEntitiesSplitAcrossBoundary places HTML entities
// (&amp;, &lt;, &gt;, &#39;) directly across the 4000-character boundary and verifies
// that chunking never cuts inside an entity.
func TestSafeChunkTelegramHTML_HTMLEntitiesSplitAcrossBoundary(t *testing.T) {
	entities := []string{"&amp;", "&lt;", "&gt;", "&#39;"}
	for _, ent := range entities {
		t.Run("Entity_"+ent, func(t *testing.T) {
			prefix := strings.Repeat("C", 3998)
			suffix := strings.Repeat("D", 500)
			input := prefix + ent + suffix

			chunks := SafeChunkTelegramHTML(input, 4000)
			require.GreaterOrEqual(t, len(chunks), 2)

			for i, chunk := range chunks {
				assert.LessOrEqual(t, len(chunk), 4096)
				assert.False(t, strings.HasSuffix(chunk, "&"), "Chunk %d ends with severed entity '&'", i)
				assert.False(t, strings.HasPrefix(chunk, ";"), "Chunk %d starts with severed entity ';'", i)
			}
		})
	}
}

// TestSafeChunkTelegramHTML_TextWithNoTags_Exceeding10000Chars tests
// pure plain text with no HTML tags exceeding 10,000 characters.
func TestSafeChunkTelegramHTML_TextWithNoTags_Exceeding10000Chars(t *testing.T) {
	rawText := strings.Repeat("Plain text sentence without any HTML tags. It contains facts and words. ", 200)
	require.Greater(t, len(rawText), 10000)

	chunks := SafeChunkTelegramHTML(rawText, 4000)
	require.GreaterOrEqual(t, len(chunks), 3)

	var recombined strings.Builder
	for i, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4000)
		assert.True(t, utf8.ValidString(chunk), "Chunk %d must be valid UTF-8", i)
		assert.NotContains(t, chunk, "<", "No tags should be introduced into plain text chunk")
		assert.NotContains(t, chunk, ">", "No tags should be introduced into plain text chunk")
		if recombined.Len() > 0 {
			recombined.WriteString(" ")
		}
		recombined.WriteString(chunk)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

