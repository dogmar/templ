// Package doc is a small Wadler/Prettier-style document IR and a width-aware
// printer. It knows nothing about templ syntax.
package doc

import "strings"

// TabWidth is the number of columns a tab counts for when measuring width.
const TabWidth = 4

// Doc is a node in the document IR.
type Doc interface{ isDoc() }

type lineKind int

const (
	lineSpace   lineKind = iota // a space when flat, a newline when broken
	lineSoft                    // nothing when flat, a newline when broken
	lineHard                    // always a newline
	lineBlank                   // always a newline followed by an empty line
	lineLiteral                 // always a newline, without indentation or trimming
)

type text struct{ s string }

type line struct{ kind lineKind }

type group struct {
	children []Doc
	hard     bool // contains a hard line, so must break
}

type indent struct {
	children []Doc
	hard     bool
}

type concat struct {
	children []Doc
	hard     bool
}

type fill struct {
	parts []Doc
	hard  bool
}

func (text) isDoc()   {}
func (line) isDoc()   {}
func (group) isDoc()  {}
func (indent) isDoc() {}
func (concat) isDoc() {}
func (fill) isDoc()   {}

// Text is literal text. It must not contain a newline: callers split
// multi-line content into Text and HardLine.
func Text(s string) Doc {
	if strings.Contains(s, "\n") {
		panic("doc.Text: text must not contain a newline: " + s)
	}
	return text{s}
}

// Line is a space when flat and a newline when broken.
func Line() Doc { return line{lineSpace} }

// SoftLine is nothing when flat and a newline when broken.
func SoftLine() Doc { return line{lineSoft} }

// HardLine is always a newline, and breaks every enclosing group.
func HardLine() Doc { return line{lineHard} }

// BlankLine is a newline followed by an empty line, and breaks every
// enclosing group.
func BlankLine() Doc { return line{lineBlank} }

// LiteralLine is always a newline, and breaks every enclosing group. The
// next line is not indented, and the line before it keeps its trailing
// whitespace, so that multi-line content can be written verbatim.
func LiteralLine() Doc { return line{lineLiteral} }

// Group lays its contents out flat if they fit on the line, else broken.
func Group(docs ...Doc) Doc { return group{docs, anyHard(docs)} }

// Indent indents line breaks within its contents by one tab.
func Indent(docs ...Doc) Doc { return indent{docs, anyHard(docs)} }

// Concat joins docs without changing layout.
func Concat(docs ...Doc) Doc { return concat{docs, anyHard(docs)} }

// Fill takes alternating content and separator docs. Each separator breaks
// only if the following content does not fit on the current line.
func Fill(parts ...Doc) Doc { return fill{parts, anyHard(parts)} }

func anyHard(docs []Doc) bool {
	for _, d := range docs {
		if hasHard(d) {
			return true
		}
	}
	return false
}

func hasHard(d Doc) bool {
	switch d := d.(type) {
	case line:
		return d.kind != lineSpace && d.kind != lineSoft
	case group:
		return d.hard
	case indent:
		return d.hard
	case concat:
		return d.hard
	case fill:
		return d.hard
	}
	return false
}

// HasHardLine reports whether d contains a HardLine or BlankLine, so that
// every group enclosing it must break.
func HasHardLine(d Doc) bool { return hasHard(d) }
