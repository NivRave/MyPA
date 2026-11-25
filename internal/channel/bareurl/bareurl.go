// Package bareurl lets a channel's markdown converter read a bare http(s) URL in running text as
// one piece, before emphasis is looked for, so the URL reaches the person exactly as written.
//
// A random token in a link (a dashboard sign-in link's base64url fragment) has "_abc_" or
// "__abc__" in it often enough; read as emphasis it lost its underscores, or had them replaced,
// and the link no longer signed anyone in (CI run 45569046251).
package bareurl

import (
	"regexp"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// attr marks an AutoLink this parser found in running text, as opposed to one written as <url>.
const attr = "mypa-bare-url"

// urlRegexp is goldmark's linkify URL pattern without its required dotted top-level domain: the
// dashboard is usually on localhost, a LAN name or an IP address.
var urlRegexp = regexp.MustCompile(`^https?://[-a-zA-Z0-9@:%._\+~#=]{1,256}(?:[/#?][-a-zA-Z0-9@:%_+.~#$!?&/=\(\);,'">\^{}\[\]` + "`" + `]*)?`)

// neverMatches matches nothing; it switches off linkify's www. and e-mail forms, which the
// converters never linked before.
var neverMatches = regexp.MustCompile(`[^\x00-\x{10FFFF}]`)

type urlParser struct{ parser.InlineParser }

func (p urlParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	n := p.InlineParser.Parse(parent, block, pc)
	if n != nil {
		n.SetAttributeString(attr, true)
	}
	return n
}

// Parser is the inline parser to add: goldmark.WithParserOptions(parser.WithInlineParsers(Parser)).
// It reads only http:// and https:// URLs. Trailing punctuation (".", ",", "_" and so on) is left
// out of the URL by goldmark and comes back as text right after it, so the written text is
// unchanged.
var Parser = util.Prioritized(urlParser{extension.NewLinkifyParser(
	extension.WithLinkifyAllowedProtocols([]string{"http:", "https:"}),
	extension.WithLinkifyURLRegexp(urlRegexp),
	extension.WithLinkifyWWWRegexp(neverMatches),
	extension.WithLinkifyEmailRegexp(neverMatches),
)}, 999)

// Is reports whether n is a URL Parser found in running text. A converter writes it back as its
// label, the text as written.
func Is(n ast.Node) bool {
	_, ok := n.AttributeString(attr)
	return ok
}
