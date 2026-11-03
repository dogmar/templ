package format

import (
	"bytes"
	"go/scanner"
	"go/token"
	"io"
	"strings"

	"github.com/a-h/templ/internal/format/doc"
	parser "github.com/a-h/templ/parser/v2"
)

// Print lays out t within width columns. The layout is render-equivalent:
// each boundary between template nodes is written so that it renders as it
// did in t (see GLOSSARY.md).
//
// Every top-level node except *parser.HTMLTemplate is written by its own
// Write. Print does not run goimports or Prettier.
func Print(t *parser.TemplateFile, width int) ([]byte, error) {
	var buf bytes.Buffer
	for _, n := range t.Header {
		if err := n.Write(&buf, 0); err != nil {
			return nil, err
		}
	}
	if err := t.Package.Write(&buf, 0); err != nil {
		return nil, err
	}
	buf.WriteString("\n\n")
	for i, n := range t.Nodes {
		var err error
		if ht, ok := n.(*parser.HTMLTemplate); ok {
			err = printTemplate(&buf, ht, width)
		} else {
			err = n.Write(&buf, 0)
		}
		if err != nil {
			return nil, err
		}
		buf.WriteString(templateFileNodeSeparator(t.Nodes, i))
	}
	return buf.Bytes(), nil
}

// templateFileNodeSeparator is the whitespace written after the top-level
// node at i, matching parser.TemplateFile.Write.
func templateFileNodeSeparator(nodes []parser.TemplateFileNode, i int) string {
	if i == len(nodes)-1 {
		return "\n"
	}
	if _, nextIsTemplate := nodes[i+1].(*parser.HTMLTemplate); nextIsTemplate {
		if e, isGo := nodes[i].(*parser.TemplateFileGoExpression); isGo {
			lines := strings.Split(e.Expression.Value, "\n")
			if strings.HasPrefix(lines[len(lines)-1], "//") {
				return "\n"
			}
		}
	}
	return "\n\n"
}

func printTemplate(buf *bytes.Buffer, t *parser.HTMLTemplate, width int) error {
	// The header is what Write produces for the template without children:
	// "templ Name(args) {\n}".
	var header bytes.Buffer
	if err := (&parser.HTMLTemplate{Expression: t.Expression}).Write(&header, 0); err != nil {
		return err
	}
	buf.WriteString(strings.TrimSuffix(header.String(), "\n}"))
	children := nonWhitespace(t.Children)
	if len(children) == 0 {
		buf.WriteString("\n}")
		return nil
	}
	body := doc.Concat(
		doc.Indent(doc.HardLine(), nodesDoc(t.Children, parser.BoundaryInElement, 1)),
		verticalWhitespace(children[len(children)-1], doc.HardLine(), 0),
		doc.Text("}"),
	)
	buf.WriteString(doc.Print(body, width))
	return nil
}

// gap is a non-whitespace node with the whitespace written before it.
type gap struct {
	node     parser.Node
	ws       parser.TrailingSpace // the whitespace before node
	newlines int                  // newlines in that whitespace
}

// gaps returns the non-whitespace nodes of nodes, each with the whitespace
// written between it and the node before it.
func gaps(nodes []parser.Node) []gap {
	var gs []gap
	var prev parser.Node
	var ws *parser.Whitespace
	for _, n := range nodes {
		if w, ok := n.(*parser.Whitespace); ok {
			ws = w
			continue
		}
		g := gap{node: n}
		if wt, ok := prev.(parser.WhitespaceTrailer); ok {
			g.ws = wt.Trailing()
			if g.ws == parser.SpaceVertical {
				g.newlines = 1
				if from, ok := endLine(prev); ok {
					if to, ok := startLine(n); ok && to > from {
						g.newlines = int(to - from)
					}
				}
			}
		}
		if ws != nil && g.ws == parser.SpaceNone {
			g.ws, _ = parser.NewTrailingSpace(ws.Value)
			g.newlines = strings.Count(ws.Value, "\n")
		}
		gs = append(gs, g)
		prev, ws = n, nil
	}
	return gs
}

func nonWhitespace(nodes []parser.Node) []parser.Node {
	var ns []parser.Node
	for _, n := range nodes {
		if _, ok := n.(*parser.Whitespace); !ok {
			ns = append(ns, n)
		}
	}
	return ns
}

// nodesDoc lays out sibling nodes whose lines are at depth, writing each
// boundary between them by its kind. ctx is the kind of node list holding
// them. The boundaries before the first node and after the last are left to
// the caller.
func nodesDoc(nodes []parser.Node, ctx parser.BoundaryContext, depth int) doc.Doc {
	gs := gaps(nodes)
	var parts []doc.Doc
	var words []doc.Doc // the Fill of the current run of text
	flush := func() {
		if len(words) > 0 {
			parts = append(parts, doc.Fill(words...))
			words = nil
		}
	}
	for i, g := range gs {
		glued := false
		if i > 0 {
			prev := gs[i-1].node
			kind := parser.ClassifyBoundary(prev, g.node, g.ws, ctx)
			if t, ok := g.node.(*parser.Text); ok && kind == parser.BoundarySoft {
				if _, prevIsText := prev.(*parser.Text); prevIsText {
					// Adjacent text joins the same Fill.
					words = append(words, textJoin(prev, t, g))
					words = append(words, textParts(t)...)
					continue
				}
			}
			flush()
			if sep := boundaryDoc(prev, g, kind, depth); sep != nil {
				parts = append(parts, sep)
			}
			glued = kind == parser.BoundaryGlued
		}
		if t, ok := g.node.(*parser.Text); ok {
			words = textParts(t)
			continue
		}
		parts = append(parts, nodeDoc(g.node, glued, depth))
	}
	flush()
	return doc.Concat(parts...)
}

// boundaryDoc writes the boundary of the given kind between prev and g.node.
func boundaryDoc(prev parser.Node, g gap, kind parser.Boundary, depth int) doc.Doc {
	// A newline next to text that starts or ends with a space would drop
	// that space: the printer trims trailing spaces, and the parser drops
	// leading ones.
	noBreak := endsWithSpace(prev) || startsWithSpace(g.node)
	switch {
	case kind == parser.BoundaryGlued:
		return nil
	case kind == parser.BoundaryFixed:
		return sourceWhitespace(g, verticalWhitespace(prev, doc.HardLine(), depth))
	case !isLaidOut(prev) || !isLaidOut(g.node):
		// Nodes printed by their own Write stay on their own lines where
		// that is safe, and otherwise keep the whitespace they had.
		if kind == parser.BoundaryIgnored && !noBreak {
			return newline(g)
		}
		return sourceWhitespace(g, verticalWhitespace(prev, newline(g), depth))
	case noBreak:
		if kind == parser.BoundarySoft {
			return doc.Text(" ")
		}
		return nil
	case g.newlines >= 2:
		return doc.BlankLine()
	case kind == parser.BoundarySoft:
		return doc.Line()
	}
	return doc.SoftLine()
}

// sourceWhitespace is the whitespace written before g.node, with vertical
// whitespace written as vertical.
func sourceWhitespace(g gap, vertical doc.Doc) doc.Doc {
	switch g.ws {
	case parser.SpaceHorizontal:
		return doc.Text(" ")
	case parser.SpaceVertical:
		return vertical
	}
	return nil
}

// verticalWhitespace is line, unless prev is text ending with a space, which
// line would trim. Then it is a newline that keeps the space, followed by
// the indentation for depth.
func verticalWhitespace(prev parser.Node, line doc.Doc, depth int) doc.Doc {
	if endsWithSpace(prev) {
		return doc.Concat(doc.LiteralLine(), doc.Text(strings.Repeat("\t", depth)))
	}
	return line
}

func newline(g gap) doc.Doc {
	if g.newlines >= 2 {
		return doc.BlankLine()
	}
	return doc.HardLine()
}

// isLaidOut reports whether the boundaries around n are laid out by this
// printer rather than kept as they were. An HTML comment is written by its
// own Write, but whitespace around it can be added or removed safely.
func isLaidOut(n parser.Node) bool {
	switch n.(type) {
	case *parser.Element, *parser.Text, *parser.StringExpression, *parser.HTMLComment:
		return true
	}
	return false
}

// nodeDoc lays out a node other than text, whose lines are at depth.
// gluedBefore reports whether the boundary before it is glued.
func nodeDoc(n parser.Node, gluedBefore bool, depth int) doc.Doc {
	switch n := n.(type) {
	case *parser.Element:
		return elementDoc(n, gluedBefore, depth)
	case *parser.StringExpression:
		return codeDoc(n.Write)
	}
	return verbatimDoc(n.Write, depth)
}

// codeDoc is the output of write at indent 0, one Text per line, with each
// following line indented to the enclosing level, except for lines inside a
// Go raw string literal, which are kept verbatim.
func codeDoc(write func(w io.Writer, indent int) error) doc.Doc {
	s := writeString(write, 0)
	literal := rawStringNewlines(s)
	var parts []doc.Doc
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		parts = append(parts, doc.Text(s[start:i]))
		if literal[i] {
			parts = append(parts, doc.LiteralLine())
		} else {
			parts = append(parts, doc.HardLine())
		}
		start = i + 1
	}
	return doc.Concat(append(parts, doc.Text(s[start:]))...)
}

// rawStringNewlines returns the offsets of the newlines in s that are inside
// a Go raw string literal. If s cannot be scanned as Go, it returns every
// newline, so that all lines are kept verbatim.
func rawStringNewlines(s string) map[int]bool {
	newlines := map[int]bool{}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(s))
	failed := false
	var sc scanner.Scanner
	sc.Init(file, []byte(s), func(token.Position, string) { failed = true }, 0)
	for !failed {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING || !strings.HasPrefix(lit, "`") {
			continue
		}
		start := file.Offset(pos)
		for i := start; i < len(s) && i < start+len(lit); i++ {
			if s[i] == '\n' {
				newlines[i] = true
			}
		}
	}
	if failed {
		for i := range len(s) {
			if s[i] == '\n' {
				newlines[i] = true
			}
		}
	}
	return newlines
}

// verbatimDoc is the output of write at depth, which starts at the current
// position and keeps its following lines exactly as written.
func verbatimDoc(write func(w io.Writer, indent int) error, depth int) doc.Doc {
	s := strings.TrimPrefix(writeString(write, depth), strings.Repeat("\t", depth))
	return linesDoc(s, doc.LiteralLine())
}

func writeString(write func(w io.Writer, indent int) error, indent int) string {
	var buf bytes.Buffer
	if err := write(&buf, indent); err != nil {
		// Write only fails when its writer does, and a bytes.Buffer
		// does not.
		panic(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// linesDoc is s with each newline replaced by newline.
func linesDoc(s string, newline doc.Doc) doc.Doc {
	lines := strings.Split(s, "\n")
	parts := make([]doc.Doc, 0, 2*len(lines))
	for i, line := range lines {
		if i > 0 {
			parts = append(parts, newline)
		}
		parts = append(parts, doc.Text(line))
	}
	return doc.Concat(parts...)
}

// elementDoc lays out an element whose lines are at depth. When gluedBefore
// is true, the element can take the tag break for the glued boundary before
// it: a line break inside its opening tag.
func elementDoc(e *parser.Element, gluedBefore bool, depth int) doc.Doc {
	open := []doc.Doc{doc.Text("<" + e.Name)}
	if len(e.Attributes) > 0 {
		open = append(open, attributesDoc(e.Attributes)...)
	} else if gluedBefore {
		open = append(open, doc.SoftLine())
	}
	children := nonWhitespace(e.Children)
	if len(children) == 0 {
		if e.IsVoidElement() {
			return doc.Group(append(open, doc.Text("/>"))...)
		}
		return doc.Group(append(open, doc.Text("></"+e.Name+">"))...)
	}
	// The opening tag is its own group, so it breaks only when it does not
	// fit itself, not when the children do.
	openTag := doc.Group(append(open, doc.Text(">"))...)
	childrenDoc := nodesDoc(e.Children, parser.BoundaryInElement, depth+1)
	first, last := children[0], children[len(children)-1]
	closeTag := doc.Text("</" + e.Name + ">")
	if hasBlockChild(children) || doc.HasHardLine(childrenDoc) {
		return doc.Concat(
			openTag,
			doc.Indent(openingEdge(first, doc.HardLine()), childrenDoc),
			closingEdge(last, doc.HardLine()),
			closeTag,
		)
	}
	return doc.Group(
		openTag,
		doc.Indent(openingEdge(first, doc.SoftLine()), childrenDoc),
		closingEdge(last, doc.SoftLine()),
		closeTag,
	)
}

// attributesDoc is the attributes of an opening tag, each on its own line
// when the tag breaks, followed by the break before the closing >.
func attributesDoc(attrs []parser.Attribute) []doc.Doc {
	var parts []doc.Doc
	sep, end := doc.Line(), doc.SoftLine()
	for _, a := range attrs {
		parts = append(parts, sep, attributeDoc(a))
		sep, end = doc.Line(), doc.SoftLine()
		if c, ok := a.(*parser.AttributeComment); ok && !c.Multiline {
			// A line comment runs to the end of the line.
			sep, end = doc.HardLine(), doc.HardLine()
		}
	}
	return []doc.Doc{doc.Indent(parts...), end}
}

// attributeDoc is the text of a's Write. Constant values render as written,
// so their newlines are kept verbatim.
func attributeDoc(a parser.Attribute) doc.Doc {
	switch a := a.(type) {
	case *parser.ConstantAttribute:
		return linesDoc(a.String(), doc.LiteralLine())
	case *parser.ConditionalAttribute:
		parts := []doc.Doc{codeDoc(func(w io.Writer, _ int) error {
			_, err := io.WriteString(w, "if "+a.Expression.Value+" {")
			return err
		})}
		parts = append(parts, attributeBlockDoc(a.Then)...)
		if len(a.Else) > 0 {
			parts = append(parts, doc.Text(" else {"))
			parts = append(parts, attributeBlockDoc(a.Else)...)
		}
		return doc.Concat(parts...)
	}
	return codeDoc(a.Write)
}

// attributeBlockDoc is the indented attributes of a conditional attribute
// branch, followed by its closing brace.
func attributeBlockDoc(attrs []parser.Attribute) []doc.Doc {
	var body []doc.Doc
	for _, a := range attrs {
		body = append(body, doc.HardLine(), attributeDoc(a))
	}
	return []doc.Doc{doc.Indent(body...), doc.HardLine(), doc.Text("}")}
}

// hasBlockChild reports whether a child always goes on its own line: a block
// element, a statement, or a node written by its own Write other than an
// HTML comment.
func hasBlockChild(children []parser.Node) bool {
	for _, c := range children {
		switch c := c.(type) {
		case *parser.Element:
			if c.IsBlockElement() {
				return true
			}
		case *parser.Text, *parser.StringExpression, *parser.HTMLComment:
		default:
			return true
		}
	}
	return false
}

// openingEdge is the break just inside an opening tag before first.
func openingEdge(first parser.Node, line doc.Doc) doc.Doc {
	if startsWithSpace(first) {
		return doc.Concat()
	}
	return line
}

// closingEdge is the break before a closing tag after last.
func closingEdge(last parser.Node, line doc.Doc) doc.Doc {
	if endsWithSpace(last) {
		return doc.Concat()
	}
	return line
}

// endLine is the source line on which n ends, excluding trailing whitespace.
func endLine(n parser.Node) (uint32, bool) {
	switch n := n.(type) {
	case *parser.Element:
		if n.CloseTagRange != nil {
			return n.CloseTagRange.To.Line, true
		}
		return n.OpenTagRange.To.Line, true
	case *parser.Text:
		return n.Range.To.Line, true
	case *parser.StringExpression:
		return n.Expression.Range.To.Line, true
	case *parser.TemplElementExpression:
		return n.Range.To.Line, true
	}
	return 0, false
}

// startLine is the source line on which n starts.
func startLine(n parser.Node) (uint32, bool) {
	var r parser.Range
	switch n := n.(type) {
	case *parser.Element:
		r = n.Range
	case *parser.Text:
		r = n.Range
	case *parser.StringExpression:
		r = n.Range
	case *parser.TemplElementExpression:
		r = n.Range
	case *parser.CallTemplateExpression:
		r = n.Range
	case *parser.ChildrenExpression:
		r = n.Range
	case *parser.IfExpression:
		r = n.Range
	case *parser.ForExpression:
		r = n.Range
	case *parser.SwitchExpression:
		r = n.Range
	case *parser.GoCode:
		r = n.Range
	case *parser.GoComment:
		r = n.Range
	case *parser.HTMLComment:
		r = n.Range
	case *parser.ScriptElement:
		r = n.Range
	case *parser.RawElement:
		r = n.Range
	case *parser.DocType:
		r = n.Range
	case *parser.Fallthrough:
		r = n.Range
	default:
		return 0, false
	}
	return r.From.Line, true
}
