package format

import (
	"strings"

	"github.com/a-h/templ/internal/format/doc"
	parser "github.com/a-h/templ/parser/v2"
)

// textParts is the words of t as alternating content and separator parts of
// a doc.Fill. A newline between two words renders as the single space it
// replaces, so each separator may break. A line-start hazard is joined to
// the word before it, so that no line starts with it. caseBody reports
// whether t is in a switch case body.
func textParts(t *parser.Text, caseBody bool) []doc.Doc {
	var parts []doc.Doc
	var content string
	for i, w := range textWords(t.Value) {
		switch {
		case i == 0:
			content = w
		case isLineStartHazard(w, caseBody):
			content += " " + w
		default:
			parts = append(parts, doc.Text(content), doc.Line())
			content = w
		}
	}
	return append(parts, doc.Text(content))
}

// textJoin is the Fill separator between adjacent text nodes prev and next
// at a soft boundary.
func textJoin(prev parser.Node, next *parser.Text, g gap, caseBody bool) doc.Doc {
	switch {
	case endsWithSpace(prev) || startsWithSpace(next), isLineStartHazard(textWords(next.Value)[0], caseBody):
		return doc.Text(" ")
	case g.newlines >= 2:
		return doc.BlankLine()
	}
	return doc.Line()
}

// textWords splits s at each single space between two other characters.
// A run of two or more spaces, or a tab, keeps its neighbours in one word,
// and leading and trailing spaces stay on the first and last word.
func textWords(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s)-1; i++ {
		if s[i] == ' ' && !isSpaceOrTab(s[i-1]) && !isSpaceOrTab(s[i+1]) {
			words = append(words, s[start:i])
			start = i + 1
		}
	}
	return append(words, s[start:])
}

func isSpaceOrTab(b byte) bool { return b == ' ' || b == '\t' }

// isLineStartHazard reports whether word would be parsed as templ syntax if
// it began a line. In a switch case body (caseBody), a line starting with
// case or default also starts the next case.
func isLineStartHazard(word string, caseBody bool) bool {
	keywords := []string{"if", "for", "switch"}
	if caseBody {
		if strings.HasPrefix(word, "default") {
			return true
		}
		keywords = append(keywords, "case")
	}
	for _, kw := range keywords {
		if word == kw || strings.HasPrefix(word, kw+" ") || strings.HasPrefix(word, kw+"\t") {
			return true
		}
	}
	return strings.HasPrefix(word, "@") || strings.HasPrefix(word, "//") || strings.HasPrefix(word, "/*")
}

// startsWithSpace reports whether n is text starting with a space or tab.
func startsWithSpace(n parser.Node) bool {
	t, ok := n.(*parser.Text)
	return ok && t.Value != "" && isSpaceOrTab(t.Value[0])
}

// endsWithSpace reports whether n is text ending with a space or tab.
func endsWithSpace(n parser.Node) bool {
	t, ok := n.(*parser.Text)
	return ok && t.Value != "" && isSpaceOrTab(t.Value[len(t.Value)-1])
}
