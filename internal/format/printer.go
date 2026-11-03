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
		doc.Indent(doc.HardLine(), nodesDoc(t.Children, siblings{ctx: parser.BoundaryInElement}, 1)),
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

// siblings is how the generator writes a list of sibling nodes.
type siblings struct {
	ctx parser.BoundaryContext
	// after is the node the generator passes as next to the last node: the
	// node after the enclosing statement. It is nil for the children of an
	// element, a template or a call.
	after parser.Node
	// caseBody reports whether the nodes are a switch case body, in which
	// a line starting with case or default starts the next case.
	caseBody bool
}

// nodesDoc lays out sibling nodes whose lines are at depth, writing each
// boundary between them by its kind. The boundaries before the first node
// and after the last are left to the caller.
func nodesDoc(nodes []parser.Node, s siblings, depth int) doc.Doc {
	gs := gaps(nodes)
	nexts := generatorNexts(nodes, s)
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
			kind := parser.ClassifyBoundary(prev, g.node, g.ws, s.ctx)
			if t, ok := g.node.(*parser.Text); ok && kind == parser.BoundarySoft {
				if _, prevIsText := prev.(*parser.Text); prevIsText {
					// Adjacent text joins the same Fill.
					words = append(words, textJoin(prev, t, g, s.caseBody))
					words = append(words, textParts(t, s.caseBody)...)
					continue
				}
			}
			if kind == parser.BoundarySoft && endsWithSpace(prev) && len(words) > 0 && !isOwnLine(g.node) {
				// The boundary's space is written at the end of the text,
				// glued to the next node, which is how it parses again.
				words[len(words)-1] = doc.Concat(words[len(words)-1], doc.Text(" "))
				kind = parser.BoundaryGlued
			}
			flush()
			if sep := boundaryDoc(prev, g, kind, depth); sep != nil {
				parts = append(parts, sep)
			}
			glued = kind == parser.BoundaryGlued
		}
		if t, ok := g.node.(*parser.Text); ok {
			words = textParts(t, s.caseBody)
			continue
		}
		parts = append(parts, nodeDoc(g.node, glued, nexts[i], depth))
	}
	flush()
	return doc.Concat(parts...)
}

// generatorNexts returns, for each non-whitespace node in nodes, the node
// the generator passes to it as next. Element children are written without
// their *Whitespace nodes, and a body without its leading and trailing ones.
func generatorNexts(nodes []parser.Node, s siblings) []parser.Node {
	written := nonWhitespace(nodes)
	if s.ctx == parser.BoundaryInBody {
		written = trimTrailingWhitespace(nodes)
	}
	var nexts []parser.Node
	for i, n := range written {
		if _, ok := n.(*parser.Whitespace); ok {
			continue
		}
		next := s.after
		if i+1 < len(written) {
			next = written[i+1]
		}
		nexts = append(nexts, next)
	}
	return nexts
}

func trimTrailingWhitespace(nodes []parser.Node) []parser.Node {
	for len(nodes) > 0 {
		if _, ok := nodes[len(nodes)-1].(*parser.Whitespace); !ok {
			break
		}
		nodes = nodes[:len(nodes)-1]
	}
	return nodes
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
	case startsWithSpace(g.node) || continuesCall(prev, g.node):
		return sourceWhitespace(g, verticalWhitespace(prev, newline(g), depth))
	case isOwnLine(prev) || isOwnLine(g.node):
		// A soft boundary renders the same as a space or a newline. Text
		// ending with a space keeps the whitespace it had, so that the
		// space does not end up at the end of a line.
		if noBreak {
			return sourceWhitespace(g, verticalWhitespace(prev, newline(g), depth))
		}
		return newline(g)
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

// continuesCall reports whether next, written on the same line as the call
// prev, would be parsed as part of the call: a space followed by . ( or [
// continues its Go expression, and a { starts its children. Only a newline
// ends the call before them.
func continuesCall(prev, next parser.Node) bool {
	if _, ok := prev.(*parser.TemplElementExpression); !ok {
		return false
	}
	switch next := next.(type) {
	case *parser.Text:
		return next.Value != "" && strings.ContainsRune(".([", rune(next.Value[0]))
	case *parser.StringExpression, *parser.ChildrenExpression, *parser.GoCode, *parser.CallTemplateExpression:
		return true
	}
	return false
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

// isOwnLine reports whether n is always written on lines of its own,
// rather than laid out inline with its siblings: statements, calls with
// children, and nodes written by their own Write other than HTML comments.
func isOwnLine(n parser.Node) bool {
	switch n := n.(type) {
	case *parser.Element, *parser.Text, *parser.StringExpression, *parser.HTMLComment:
		return false
	case *parser.TemplElementExpression:
		return len(n.Children) > 0
	}
	return true
}

// nodeDoc lays out a node other than text, whose lines are at depth.
// gluedBefore reports whether the boundary before it is glued, and next is
// the node the generator passes to it as next.
func nodeDoc(n parser.Node, gluedBefore bool, next parser.Node, depth int) doc.Doc {
	switch n := n.(type) {
	case *parser.Element:
		return elementDoc(n, gluedBefore, depth)
	case *parser.StringExpression:
		return codeDoc(n.Write)
	case *parser.IfExpression:
		return ifDoc(n, next, depth)
	case *parser.ForExpression:
		return doc.Concat(
			headerDoc("for "+n.Expression.Value+" {"),
			bodyDoc(n.Children, next, depth, false),
			bodyEndDoc(n.Children, next, depth),
			doc.Text("}"),
		)
	case *parser.SwitchExpression:
		return switchDoc(n, next, depth)
	case *parser.TemplElementExpression:
		return callDoc(n, depth)
	case *parser.CallTemplateExpression:
		// Write rewrites this to a templ element expression, which renders
		// the whitespace around it differently, so the source syntax is kept.
		return headerDoc("{! " + n.Expression.Value + " }")
	}
	return verbatimDoc(n.Write, depth)
}

// headerDoc is the source line of a statement, as its Write writes it: Go
// code that is not reformatted, so its following lines are kept verbatim.
func headerDoc(s string) doc.Doc {
	return linesDoc(s, doc.LiteralLine())
}

func ifDoc(n *parser.IfExpression, next parser.Node, depth int) doc.Doc {
	parts := []doc.Doc{
		headerDoc("if " + n.Expression.Value + " {"),
		bodyDoc(n.Then, next, depth, false),
		bodyEndDoc(n.Then, next, depth),
	}
	for _, e := range n.ElseIfs {
		parts = append(parts,
			headerDoc("} else if "+e.Expression.Value+" {"),
			bodyDoc(e.Then, next, depth, false),
			bodyEndDoc(e.Then, next, depth),
		)
	}
	if len(n.Else) > 0 {
		parts = append(parts,
			doc.Text("} else {"),
			bodyDoc(n.Else, next, depth, false),
			bodyEndDoc(n.Else, next, depth),
		)
	}
	return doc.Concat(append(parts, doc.Text("}"))...)
}

// switchDoc lays out a switch with each case indented under it, and each
// case body indented under its case.
func switchDoc(n *parser.SwitchExpression, next parser.Node, depth int) doc.Doc {
	var cases []doc.Doc
	end := doc.HardLine()
	for i, c := range n.Cases {
		cases = append(cases, end, headerDoc(c.Expression.Value), bodyDoc(c.Children, next, depth+1, true))
		if i < len(n.Cases)-1 {
			end = bodyEndDoc(c.Children, next, depth+1)
		} else {
			end = bodyEndDoc(c.Children, next, depth)
		}
	}
	return doc.Concat(
		headerDoc("switch "+n.Expression.Value+" {"),
		doc.Indent(cases...),
		end,
		doc.Text("}"),
	)
}

// callDoc lays out a templ element expression, and its children, if any,
// as a body.
func callDoc(n *parser.TemplElementExpression, depth int) doc.Doc {
	call := verbatimDoc((&parser.TemplElementExpression{Expression: n.Expression}).Write, depth)
	if len(n.Children) == 0 {
		return call
	}
	return doc.Concat(
		call,
		doc.Text(" {"),
		bodyDoc(n.Children, nil, depth, false),
		bodyEndDoc(n.Children, nil, depth),
		doc.Text("}"),
	)
}

// bodyDoc lays out the body of a statement whose first line is at depth:
// its nodes, starting on the next line, indented. next is the node the
// generator passes to the last of them. The generator drops the body's
// leading whitespace, so the boundary before the first node is ignored.
func bodyDoc(nodes []parser.Node, next parser.Node, depth int, caseBody bool) doc.Doc {
	if len(nonWhitespace(nodes)) == 0 {
		return doc.Concat()
	}
	s := siblings{ctx: parser.BoundaryInBody, after: next, caseBody: caseBody}
	return doc.Indent(doc.HardLine(), doc.Group(nodesDoc(nodes, s, depth+1)))
}

// bodyEndDoc is the boundary between the last node of a body and the line
// at depth that closes it, given the node next that the generator passes to
// that last node.
func bodyEndDoc(nodes []parser.Node, next parser.Node, depth int) doc.Doc {
	children := nonWhitespace(nodes)
	if len(children) == 0 {
		return doc.HardLine()
	}
	last := children[len(children)-1]
	g := gap{node: next}
	if wt, ok := last.(parser.WhitespaceTrailer); ok {
		g.ws = wt.Trailing()
	}
	// The generator drops a body's trailing *Whitespace node, so the
	// boundary is classified as at the end of an element.
	switch parser.ClassifyBoundary(last, next, g.ws, parser.BoundaryInElement) {
	case parser.BoundaryGlued:
		return doc.Concat()
	case parser.BoundaryFixed:
		if ws := sourceWhitespace(g, verticalWhitespace(last, doc.HardLine(), depth)); ws != nil {
			return ws
		}
		return doc.Concat()
	}
	return verticalWhitespace(last, doc.HardLine(), depth)
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
	childrenDoc := nodesDoc(e.Children, siblings{ctx: parser.BoundaryInElement}, depth+1)
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
// so their newlines are kept verbatim, and so are those of comments.
func attributeDoc(a parser.Attribute) doc.Doc {
	switch a := a.(type) {
	case *parser.ConstantAttribute:
		return linesDoc(a.String(), doc.LiteralLine())
	case *parser.AttributeComment:
		// Like other comments, a multi-line comment is kept verbatim.
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
// element, or a node that is always written on lines of its own.
func hasBlockChild(children []parser.Node) bool {
	for _, c := range children {
		if e, ok := c.(*parser.Element); ok && e.IsBlockElement() || isOwnLine(c) {
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
	case *parser.GoCode:
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
