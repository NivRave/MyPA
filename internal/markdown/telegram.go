package markdown

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

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
