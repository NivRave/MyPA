package orchestrator

import (
	"strings"
	"unicode"
)

// maxTrivialTokens is the longest message (in words, after stripping punctuation and emoji)
// that can be classified as a trivial acknowledgement.
const maxTrivialTokens = 3

// trivialTokens are filler words that carry no retrievable content (English + Hebrew).
var trivialTokens = map[string]bool{
	// English
	"ok": true, "okay": true, "k": true, "kk": true, "thanks": true, "thank": true, "you": true,
	"thx": true, "ty": true, "tnx": true, "cool": true, "great": true, "nice": true, "sure": true,
	"yes": true, "yep": true, "yeah": true, "yup": true, "no": true, "nope": true, "got": true,
	"it": true, "np": true, "lol": true, "haha": true, "perfect": true, "awesome": true,
	"good": true, "fine": true, "alright": true, "bye": true, "hi": true, "hello": true,
	// Hebrew
	"תודה": true, "רבה": true, "כן": true, "לא": true, "סבבה": true, "אוקיי": true, "אוקי": true,
	"יופי": true, "מעולה": true, "אחלה": true, "בסדר": true, "טוב": true, "מצוין": true,
	"מושלם": true, "שלום": true, "היי": true, "ביי": true, "תותח": true,
}

// isTrivialMessage reports whether text is an acknowledgement with no content worth embedding
// (e.g. "thanks!", "ok 👍", "תודה רבה"). Every remaining word must be filler — a short message
// with a content word ("remind me tomorrow") is NOT trivial. Empty / emoji-only text is trivial.
func isTrivialMessage(text string) bool {
	var tokens []string
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		tok := strings.TrimFunc(raw, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	if len(tokens) > maxTrivialTokens {
		return false
	}
	for _, t := range tokens {
		if !trivialTokens[t] {
			return false
		}
	}
	return true
}
