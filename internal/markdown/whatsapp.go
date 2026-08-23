package markdown

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ToWhatsApp converts markdown text to WhatsApp specific formatting.
func ToWhatsApp(source string) string {
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
				buf.WriteString(val)
				if node.HardLineBreak() || node.SoftLineBreak() {
					buf.WriteString("\n")
				}
			}
		case *ast.String:
			if entering {
				buf.Write(node.Value)
			}
		case *ast.Emphasis:
			if entering {
				if node.Level == 2 {
					buf.WriteString("*")
				} else {
					buf.WriteString("_")
				}
			} else {
				if node.Level == 2 {
					buf.WriteString("*")
				} else {
					buf.WriteString("_")
				}
			}
		case *ast.Heading:
			if entering {
				buf.WriteString("*")
			} else {
				buf.WriteString("*\n\n")
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
				buf.WriteString(indent + "- ")
			} else {
				buf.WriteString("\n")
			}
		case *ast.CodeSpan:
			if entering {
				buf.WriteString("```")
			} else {
				buf.WriteString("```")
			}
		case *ast.FencedCodeBlock:
			if entering {
				buf.WriteString("```\n")
				lines := node.Lines()
				for i := 0; i < lines.Len(); i++ {
					line := lines.At(i)
					buf.Write(line.Value(src))
				}
			} else {
				buf.WriteString("```\n\n")
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if entering {
				// WhatsApp doesn't support named links, we'll append the URL after the text
			} else {
				dest := string(node.Destination)
				if dest != "" {
					buf.WriteString(fmt.Sprintf(" (%s)", dest))
				}
			}
		case *ast.AutoLink:
			if entering {
				buf.Write(node.URL(src))
			}
		}
		return ast.WalkContinue, nil
	})

	return strings.TrimSpace(buf.String())
}
