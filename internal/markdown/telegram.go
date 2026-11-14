package markdown

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var (
	htmlTagRegex            = regexp.MustCompile(`^</?([a-zA-Z0-9]+)(\s+[^>]*)?>`)
	// htmlEntityRegex is handled via telegramHTMLEntityRegex below
	telegramHTMLTagRegex    = regexp.MustCompile(`(?i)</?(?:b|strong|i|em|u|ins|s|strike|del|code|pre|blockquote|tg-spoiler|tg-emoji|span|a)(?:\s+[^>]*)?>`)
	telegramHTMLEntityRegex = regexp.MustCompile(`&(?:[a-zA-Z]+|#[0-9]+|#x[0-9a-fA-F]+);`)
)

// IsTelegramHTML returns true if text is already formatted as Telegram HTML
// by verifying the presence of valid Telegram HTML tags or standard HTML character entities.
func IsTelegramHTML(text string) bool {
	if strings.Contains(text, "&") && telegramHTMLEntityRegex.MatchString(text) {
		return true
	}
	if strings.Contains(text, "<") && telegramHTMLTagRegex.MatchString(text) {
		return true
	}
	return false
}

// ToTelegramHTML converts markdown text to Telegram HTML.
func ToTelegramHTML(source string) string {
	src := []byte(source)
	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(src))

	var buf bytes.Buffer
	listDepth := 0

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Text:
			if entering {
				val := string(node.Segment.Value(src))
				if node.IsRaw() {
					buf.WriteString(val)
				} else {
					buf.WriteString(html.EscapeString(val))
				}
				if node.HardLineBreak() || node.SoftLineBreak() {
					buf.WriteString("\n")
				}
			}
		case *ast.String:
			if entering {
				buf.WriteString(html.EscapeString(string(node.Value)))
			}
		case *ast.Emphasis:
			if entering {
				if node.Level == 2 {
					buf.WriteString("<b>")
				} else {
					buf.WriteString("<i>")
				}
			} else {
				if node.Level == 2 {
					buf.WriteString("</b>")
				} else {
					buf.WriteString("</i>")
				}
			}
		case *ast.Heading:
			if entering {
				buf.WriteString("<b>")
			} else {
				buf.WriteString("</b>\n\n")
			}
		case *ast.Paragraph:
			if !entering {
				if _, ok := node.Parent().(*ast.ListItem); !ok {
					buf.WriteString("\n\n")
				}
			}
		case *ast.List:
			if entering {
				listDepth++
			} else {
				listDepth--
				if listDepth == 0 {
					buf.WriteString("\n")
				}
			}
		case *ast.ListItem:
			if entering {
				indent := strings.Repeat("  ", listDepth-1)
				buf.WriteString(indent + "• ")
			} else {
				buf.WriteString("\n")
			}
		case *ast.CodeSpan:
			if entering {
				buf.WriteString("<code>")
			} else {
				buf.WriteString("</code>")
			}
		case *ast.FencedCodeBlock:
			if entering {
				buf.WriteString("<pre><code>")
				lines := node.Lines()
				for i := 0; i < lines.Len(); i++ {
					line := lines.At(i)
					buf.WriteString(html.EscapeString(string(line.Value(src))))
				}
			} else {
				buf.WriteString("</code></pre>\n\n")
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if entering {
				buf.WriteString(fmt.Sprintf("<a href=\"%s\">", html.EscapeString(string(node.Destination))))
			} else {
				buf.WriteString("</a>")
			}
		case *ast.AutoLink:
			if entering {
				url := html.EscapeString(string(node.URL(src)))
				buf.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>", url, url))
			}
		}
		return ast.WalkContinue, nil
	})

	return strings.TrimSpace(buf.String())
}

// TagSpan represents an open HTML tag with its full opening string (e.g. `<a href="...">`)
type TagSpan struct {
	Name     string
	FullOpen string
}

// SafeChunkTelegramHTML splits Telegram HTML into chunks <= maxLen (default 4000)
// on paragraph / newline / sentence boundaries, preserving balanced HTML tags across chunks.
func SafeChunkTelegramHTML(htmlText string, maxLen int) []string {
	if maxLen <= 0 || maxLen > 4096 {
		maxLen = 4000
	}
	htmlText = strings.TrimSpace(htmlText)
	if len(htmlText) <= maxLen {
		return []string{htmlText}
	}

	var chunks []string
	var activeTags []TagSpan
	remaining := htmlText

	for len(remaining) > 0 {
		reopenOverhead := 0
		closingOverhead := 0
		for _, tag := range activeTags {
			reopenOverhead += len(tag.FullOpen)
			closingOverhead += len(tag.Name) + 3 // </name>
		}

		availableForContent := maxLen - reopenOverhead - closingOverhead
		if availableForContent < 20 {
			availableForContent = maxLen / 2
		}

		if len(remaining) <= availableForContent {
			var chunkBuf bytes.Buffer
			for _, tag := range activeTags {
				chunkBuf.WriteString(tag.FullOpen)
			}
			chunkBuf.WriteString(remaining)
			remainingTags := updateActiveTags(activeTags, remaining)
			for i := len(remainingTags) - 1; i >= 0; i-- {
				chunkBuf.WriteString(fmt.Sprintf("</%s>", remainingTags[i].Name))
			}
			chunks = append(chunks, chunkBuf.String())
			break
		}

		targetCut := availableForContent
		if targetCut > len(remaining) {
			targetCut = len(remaining)
		}

		searchWindow := remaining[:targetCut]
		var breakIdx int

		// 1. Paragraph break \n\n
		if idx := strings.LastIndex(searchWindow, "\n\n"); idx > targetCut-800 && idx > 0 {
			breakIdx = idx + 2
		} else if idx := strings.LastIndex(searchWindow, "\n"); idx > targetCut-500 && idx > 0 {
			breakIdx = idx + 1
		} else if idx := strings.LastIndex(searchWindow, ". "); idx > targetCut-300 && idx > 0 {
			breakIdx = idx + 2
		} else if idx := strings.LastIndex(searchWindow, " "); idx > targetCut-150 && idx > 0 {
			breakIdx = idx + 1
		} else {
			breakIdx = targetCut
		}

		breakIdx = adjustBreakOutsideTags(remaining, breakIdx)

		// Ensure breakIdx does not split a multi-byte UTF-8 rune (Hebrew 2-byte, CJK 3-byte, Emoji 4-byte)
		for breakIdx > 0 && breakIdx < len(remaining) && !utf8.RuneStart(remaining[breakIdx]) {
			breakIdx--
		}

		// Progress guarantee: if breakIdx backtracked to 0, advance by at least one full rune
		if breakIdx <= 0 && len(remaining) > 0 {
			_, width := utf8.DecodeRuneInString(remaining)
			if width <= 0 {
				width = 1
			}
			breakIdx = width
		}

		chunkContent := strings.TrimSpace(remaining[:breakIdx])
		remaining = strings.TrimSpace(remaining[breakIdx:])

		tagsOpenAtStart := append([]TagSpan(nil), activeTags...)
		tagsOpenAtEnd := updateActiveTags(tagsOpenAtStart, chunkContent)

		var chunkBuf bytes.Buffer
		for _, tag := range tagsOpenAtStart {
			chunkBuf.WriteString(tag.FullOpen)
		}
		chunkBuf.WriteString(chunkContent)
		for i := len(tagsOpenAtEnd) - 1; i >= 0; i-- {
			chunkBuf.WriteString(fmt.Sprintf("</%s>", tagsOpenAtEnd[i].Name))
		}
		chunks = append(chunks, chunkBuf.String())

		activeTags = tagsOpenAtEnd
	}

	return chunks
}

func adjustBreakOutsideTags(s string, idx int) int {
	if idx >= len(s) {
		return len(s)
	}
	// If idx falls inside '<...>'
	lastOpen := strings.LastIndex(s[:idx], "<")
	lastClose := strings.LastIndex(s[:idx], ">")
	if lastOpen > lastClose {
		// Inside tag: break before '<'
		if lastOpen > 0 {
			return lastOpen
		}
		// If < is at start, break after '>'
		if nextClose := strings.Index(s[idx:], ">"); nextClose != -1 {
			return idx + nextClose + 1
		}
	}
	// If idx falls inside '&...;'
	lastAmp := strings.LastIndex(s[:idx], "&")
	lastSemi := strings.LastIndex(s[:idx], ";")
	if lastAmp > lastSemi && idx-lastAmp < 10 {
		return lastAmp
	}
	return idx
}

func updateActiveTags(current []TagSpan, fragment string) []TagSpan {
	stack := append([]TagSpan(nil), current...)
	i := 0
	for i < len(fragment) {
		if fragment[i] == '<' {
			loc := htmlTagRegex.FindStringSubmatchIndex(fragment[i:])
			if len(loc) >= 4 {
				tagStr := fragment[i : i+loc[1]]
				tagName := strings.ToLower(fragment[i+loc[2] : i+loc[3]])
				isClosing := strings.HasPrefix(tagStr, "</")

				if isClosing {
					// Pop matching tag from stack
					for j := len(stack) - 1; j >= 0; j-- {
						if stack[j].Name == tagName {
							stack = append(stack[:j], stack[j+1:]...)
							break
						}
					}
				} else {
					stack = append(stack, TagSpan{Name: tagName, FullOpen: tagStr})
				}
				i += loc[1]
				continue
			}
		}
		i++
	}
	return stack
}
