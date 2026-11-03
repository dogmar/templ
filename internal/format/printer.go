package format

import (
	"bytes"
	goformat "go/format"
	"go/scanner"
	"go/token"
	"io"
	"slices"
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
	// A top-level Go expression that is only whitespace, such as a stray
	// Unicode line separator, generates nothing, and writing it would add a
	// blank line on every pass.
	nodes := slices.DeleteFunc(slices.Clone(t.Nodes), func(n parser.TemplateFileNode) bool {
		e, ok := n.(*parser.TemplateFileGoExpression)
		return ok && strings.TrimSpace(e.Expression.Value) == ""
	})
	if t.Package.Expression.Value == "" && len(nodes) == 0 {
		// A file with no package and no templates has nothing to separate,
		// so keep only the header, trimmed. Otherwise every pass would add
		// blank lines.
		out := bytes.TrimSpace(buf.Bytes())
		if len(out) > 0 {
			out = append(out, '\n')
		}
		// Trimming can move a package clause the header holds, such as one
		// after a space, to the start of its line, where it is read as the
		// package clause, so the output is printed as it parses again.
		if reparsed, err := parser.ParseString(string(out)); err == nil && reparsed.Package.Expression.Value != "" {
			return Print(reparsed, width)
		}
		return out, nil
	}
	// The package clause runs to the end of its line, so it keeps a
	// carriage return before a CRLF line ending, which would make the
	// printed clause end in a CRLF and lose it on the next pass.
	buf.WriteString(strings.TrimRight(t.Package.Expression.Value, " \t\r"))
	buf.WriteString("\n\n")
	for i, n := range nodes {
		var err error
		if ht, ok := n.(*parser.HTMLTemplate); ok {
			err = printHTMLTemplate(&buf, ht, width)
		} else if st, ok := n.(*parser.ScriptTemplate); ok {
			err = writeScriptTemplate(&buf, st)
		} else if css, ok := n.(*parser.CSSTemplate); ok {
			err = writeCSSTemplate(&buf, css)
		} else {
			err = n.Write(&buf, 0)
		}
		if err != nil {
			return nil, err
		}
		buf.WriteString(templateFileNodeSeparator(nodes, i))
	}
	return buf.Bytes(), nil
}

// writeScriptTemplate writes s as its Write does, unless formatting its
// parameters would change the parameter names the generator writes into
// the script's JavaScript, such as by removing a trailing comma. Then the
// parameters are written as they are.
func writeScriptTemplate(w io.Writer, s *parser.ScriptTemplate) error {
	formatted, err := goformat.Source([]byte("func " + s.Name.Value + "(" + s.Parameters.Value + ")"))
	if err != nil {
		return s.Write(w, 0)
	}
	params := strings.TrimPrefix(string(formatted), "func "+s.Name.Value+"(")
	params = strings.TrimSuffix(params, ")")
	if scriptParameterNames(params) == scriptParameterNames(s.Parameters.Value) {
		return s.Write(w, 0)
	}
	_, err = io.WriteString(w, "script "+s.Name.Value+"("+s.Parameters.Value+") {\n"+s.Value+"}")
	return err
}

// writeCSSTemplate writes css as its Write does, except that a newline at
// either edge of a property's expression is kept, as stringExpressionDoc
// keeps it: in the generated code it lays the call around the expression
// out on lines of its own.
func writeCSSTemplate(w io.Writer, css *parser.CSSTemplate) error {
	var header bytes.Buffer
	if err := (&parser.CSSTemplate{Expression: css.Expression}).Write(&header, 0); err != nil {
		return err
	}
	if _, err := io.WriteString(w, strings.TrimSuffix(header.String(), "}")); err != nil {
		return err
	}
	for _, p := range css.Properties {
		e, ok := p.(*parser.ExpressionCSSProperty)
		var leading, trimmed, trailing string
		if ok {
			leading, trimmed, trailing = splitEdges(e.Value.Expression.Value)
		}
		if !ok || !strings.Contains(leading+trailing, "\n") {
			if err := p.Write(w, 1); err != nil {
				return err
			}
			continue
		}
		edge := func(ws string) string {
			if n := strings.Count(ws, "\n"); n > 0 {
				return strings.Repeat("\n", n)
			}
			return " "
		}
		if _, err := io.WriteString(w, "\t"+e.Name+": {"+edge(leading)+trimmed+edge(trailing)+"};\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "}")
	return err
}

// splitEdges splits v into its leading whitespace, the rest trimmed of
// whitespace, and its trailing whitespace.
func splitEdges(v string) (leading, trimmed, trailing string) {
	trimmed = strings.TrimSpace(v)
	i := strings.Index(v, trimmed)
	return v[:i], trimmed, v[i+len(trimmed):]
}

// scriptParameterNames is the parameter list the generator writes for the
// parameters of a script template: the first word of each comma-separated
// parameter.
func scriptParameterNames(parameters string) string {
	var names []string
	for _, p := range strings.Split(parameters, ",") {
		names = append(names, strings.TrimSpace(strings.Split(strings.TrimSpace(p), " ")[0]))
	}
	return strings.Join(names, ", ")
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

func printHTMLTemplate(buf *bytes.Buffer, t *parser.HTMLTemplate, width int) error {
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
		doc.Indent(doc.HardLine(), linesGroup(children, nodesDoc(t.Children, siblings{ctx: parser.BoundaryInElement}, 1))),
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
//
// A run of inline siblings is one Fill, like Prettier's JSX children: the
// words of text and the inline nodes share lines, each separator breaking
// only when what follows it does not fit. A node that goes on lines of its
// own (see isOwnLine), a block element, and a node whose layout contains a
// hard line end the run.
func nodesDoc(nodes []parser.Node, s siblings, depth int) doc.Doc {
	gs := gaps(nodes)
	nexts := generatorNexts(nodes, s)
	var parts []doc.Doc
	var words []doc.Doc // the Fill of the current run of inline siblings
	flush := func() {
		if len(words) > 0 {
			parts = append(parts, doc.Fill(words...))
			words = nil
		}
	}
	// join appends contents to the Fill, the first glued to its last content.
	join := func(contents []doc.Doc) {
		words[len(words)-1] = doc.Concat(words[len(words)-1], contents[0])
		words = append(words, contents[1:]...)
	}
	prevBreaks := false // the previous node ended a run
	prevJoined := false // the previous node is text on one line with the text before it
	for i, g := range gs {
		t, isText := g.node.(*parser.Text)
		var prev parser.Node
		kind := parser.BoundaryIgnored
		if i > 0 {
			prev = gs[i-1].node
			kind = parser.ClassifyBoundary(prev, g.node, g.ws, s.ctx)
		}
		// Text that starts with a keyword keeps the newline after it while
		// it still starts a text node, so is not joined on one line to
		// the text before it.
		keepsNewline := kind == parser.BoundarySoft && g.ws == parser.SpaceVertical && startsWithKeyword(prev) && !prevJoined
		prevJoined = false
		if _, prevIsText := prev.(*parser.Text); prevIsText && isText && kind == parser.BoundarySoft {
			// Adjacent text joins the same run of words.
			sep := textJoin(prev, t, g, s.caseBody)
			if keepsNewline {
				sep = verticalWhitespace(prev, newline(g), depth)
			}
			prevJoined = sep == doc.Text(" ")
			words = append(words, sep)
			words = append(words, textParts(t, s.caseBody)...)
			continue
		}
		if kind == parser.BoundarySoft && endsWithSpace(prev) && !isOwnLine(g.node) && !keepsNewline {
			// The boundary's space is written at the end of the text, glued
			// to the next node, which is how it parses again. Text is never
			// a breaking node, so it is the last content of the Fill.
			words[len(words)-1] = doc.Concat(words[len(words)-1], doc.Text(" "))
			kind = parser.BoundaryGlued
		}
		boundary := func(ownLine bool) doc.Doc {
			if keepsNewline {
				return verticalWhitespace(prev, newline(g), depth)
			}
			return boundaryDoc(prev, g, kind, depth, ownLine, s.caseBody)
		}
		var contents []doc.Doc
		breaks := false
		if isText {
			contents = textParts(t, s.caseBody)
		} else {
			d := nodeDoc(g.node, kind == parser.BoundaryGlued, nexts[i], depth)
			contents = []doc.Doc{d}
			breaks = isOwnLine(g.node) || isBlockElement(g.node) || doc.HasHardLine(d)
		}
		switch {
		case i == 0:
		case kind == parser.BoundaryGlued && len(words) > 0 && !isOwnLine(g.node):
			// A node glued to the content before it is part of that
			// content, so that the Fill breaks before both together.
			join(contents)
			prevBreaks = false
			continue
		case !prevBreaks && !breaks:
			if sep := boundary(false); sep != nil {
				words = append(words, sep)
				words = append(words, contents...)
			} else {
				join(contents)
			}
			continue
		default:
			flush()
			if sep := boundary(true); sep != nil {
				parts = append(parts, sep)
			}
		}
		if breaks {
			parts = append(parts, contents[0])
		} else {
			words = contents
		}
		prevBreaks = breaks
	}
	flush()
	return doc.Concat(parts...)
}

// startsWithHazard reports whether n is text whose first word is a
// line-start hazard.
func startsWithHazard(n parser.Node, caseBody bool) bool {
	t, ok := n.(*parser.Text)
	return ok && isLineStartHazard(textWords(t.Value)[0], caseBody)
}

// startsWithKeyword reports whether n is text starting with if, for or
// switch followed by a space or the end of the text. Every text node starts
// where a node is parsed, so the parser read it as text only because the
// statement it starts did not parse: typically because its line ended
// before the statement's opening brace. A newline after it is kept, so that
// joining the next line to it cannot complete the statement.
func startsWithKeyword(n parser.Node) bool {
	t, ok := n.(*parser.Text)
	return ok && startsWithWord(t.Value, statementKeywords...)
}

// isBlockElement reports whether n is an element laid out as a block, on
// lines of its own: a block element other than <br>. The boundaries around
// <br> are ignored, as for any block element, so it can go anywhere on a
// line, and it is laid out inline, like the line break it renders.
func isBlockElement(n parser.Node) bool {
	e, ok := n.(*parser.Element)
	return ok && e.IsBlockElement() && e.Name != "br"
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
// ownLine reports whether either of them goes on lines of its own; otherwise
// the boundary is a separator of a Fill. caseBody reports whether the nodes
// are a switch case body.
func boundaryDoc(prev parser.Node, g gap, kind parser.Boundary, depth int, ownLine, caseBody bool) doc.Doc {
	// A newline next to text that starts or ends with a space would drop
	// that space: the printer trims trailing spaces, and the parser drops
	// leading ones.
	noBreak := endsWithSpace(prev) || startsWithSpace(g.node)
	switch {
	case kind == parser.BoundaryGlued:
		return nil
	case kind == parser.BoundaryFixed:
		return sourceWhitespace(g, verticalWhitespace(prev, doc.HardLine(), depth))
	case continuesCall(prev, g.node):
		// Only a newline ends the call: a horizontal space here (which a
		// whitespace rune other than a space can leave) would be read as
		// part of the call.
		return verticalWhitespace(prev, newline(g), depth)
	case startsWithSpace(g.node):
		return sourceWhitespace(g, verticalWhitespace(prev, newline(g), depth))
	case ownLine:
		// A soft boundary renders the same as a space or a newline. Text
		// ending with a space keeps the whitespace it had, so that the
		// space does not end up at the end of a line.
		if noBreak {
			return sourceWhitespace(g, verticalWhitespace(prev, newline(g), depth))
		}
		return newline(g)
	case noBreak, startsWithHazard(g.node, caseBody):
		// A line must not start with a line-start hazard either.
		if kind == parser.BoundarySoft {
			return doc.Text(" ")
		}
		return nil
	case g.newlines >= 2:
		return doc.BlankLine()
	case kind == parser.BoundarySoft:
		return doc.Line()
	case g.ws == parser.SpaceVertical:
		// Like a newline between JSX children, a newline that does not
		// render is kept, so that components written one per line stay so.
		return doc.HardLine()
	case g.ws == parser.SpaceHorizontal:
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
		return stringExpressionDoc(n)
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
	case *parser.GoCode:
		if !gofmtKeepsTokens(n.Expression.Value) {
			// Write would drop tokens, such as an empty statement, that
			// lay out the generated code, so the code is kept as written.
			return linesDoc("{{ "+strings.TrimSpace(n.Expression.Value)+" }}", doc.LiteralLine())
		}
		return verbatimDoc(multilineAsReparsed(n).Write, depth)
	}
	return verbatimDoc(n.Write, depth)
}

// gofmtKeepsTokens reports whether formatting the Go code s keeps its
// tokens, counting explicit semicolons but not those a newline inserts. It
// is true if s cannot be formatted, since Write then keeps s as it is.
func gofmtKeepsTokens(s string) bool {
	formatted, err := goformat.Source([]byte(s))
	if err != nil {
		return true
	}
	return slices.Equal(goTokens(s), goTokens(string(formatted)))
}

// goTokens returns the tokens of the Go code s, with the semicolons that
// a newline or the end of s inserts left out.
func goTokens(s string) []string {
	var tokens []string
	fset := token.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile("", fset.Base(), len(s)), []byte(s), nil, scanner.ScanComments)
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			return tokens
		}
		if tok == token.SEMICOLON && lit != ";" {
			continue
		}
		tokens = append(tokens, tok.String()+lit)
	}
}

// multilineAsReparsed returns n, or a copy of it whose Multiline is whether
// its code, formatted, spans more than one line, which is how the parser
// reads it again: the parser sets Multiline when the code itself spans
// lines, not when the braces are on lines of their own. Single-line code
// formatted on more than one line, such as a label, written single-line
// would become multi-line on the next pass, and multi-line code that formats
// onto one line, written multi-line, would become single-line.
func multilineAsReparsed(n *parser.GoCode) *parser.GoCode {
	src, err := goformat.Source([]byte(n.Expression.Value))
	if err != nil {
		return n
	}
	multiline := bytes.Contains(src, []byte("\n"))
	if multiline == n.Multiline {
		return n
	}
	c := *n
	c.Multiline = multiline
	return &c
}

// stringExpressionDoc is n as its Write writes it, except that the newlines
// at the edges of its expression are kept. The expression is Go code in the
// generated code, where a newline after the opening parenthesis of a call or
// after a trailing comma lays the call out on lines of its own. The braces
// on lines of their own go at the enclosing level, outside the code, so
// that the code's first line and closing line do not count as its
// indentation (see codeDoc).
func stringExpressionDoc(n *parser.StringExpression) doc.Doc {
	leading, trimmed, trailing := splitEdges(n.Expression.Value)
	if trimmed == "" {
		return doc.Text("{ }")
	}
	edge := func(ws string) doc.Doc {
		switch strings.Count(ws, "\n") {
		case 0:
			return doc.Text(" ")
		case 1:
			return doc.HardLine()
		}
		return doc.BlankLine()
	}
	code := trimmed
	if !strings.Contains(leading, "\n") {
		code = "{ " + trimmed
	}
	parts := []doc.Doc{codeDoc(func(w io.Writer, _ int) error {
		_, err := io.WriteString(w, code)
		return err
	})}
	if strings.Contains(leading, "\n") {
		parts = append([]doc.Doc{doc.Text("{"), edge(leading)}, parts...)
	}
	return doc.Concat(append(parts, edge(trailing), doc.Text("}"))...)
}

// headerDoc is the source line of a statement, as its Write writes it: Go
// code that is not reformatted, so its following lines are kept verbatim.
func headerDoc(s string) doc.Doc {
	return linesDoc(s, doc.LiteralLine())
}

// ifDoc lays out an if statement with each body indented under its header.
// The parser only reads else after a closing brace at the start of its
// line when the body before it is empty, so then the brace is not
// indented. An empty else block is kept: the parser reads text starting
// with else after an if statement as its else branch, and the block is
// what separates them.
func ifDoc(n *parser.IfExpression, next parser.Node, depth int) doc.Doc {
	parts := []doc.Doc{
		headerDoc("if " + n.Expression.Value + " {"),
		bodyDoc(n.Then, next, depth, false),
	}
	before := n.Then
	for _, e := range n.ElseIfs {
		parts = append(parts,
			bodyEndDoc(before, next, depth),
			headerDoc("} else if "+e.Expression.Value+" {"),
			bodyDoc(e.Then, next, depth, false),
		)
		before = e.Then
	}
	if len(n.Else) > 0 || n.ElseRange != (parser.Range{}) {
		end := bodyEndDoc(before, next, depth)
		if len(nonWhitespace(before)) == 0 {
			end = doc.LiteralLine()
		}
		parts = append(parts,
			end,
			doc.Text("} else {"),
			bodyDoc(n.Else, next, depth, false),
		)
		before = n.Else
	}
	return doc.Concat(append(parts, bodyEndDoc(before, next, depth), doc.Text("}"))...)
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
	write := (&parser.TemplElementExpression{Expression: n.Expression}).Write
	call := verbatimDoc(write, depth)
	written := strings.TrimPrefix(writeString(write, 0), "@")
	rewrite := (&parser.TemplElementExpression{Expression: parser.Expression{Value: written}}).Write
	rewritten := strings.TrimPrefix(writeString(rewrite, 0), "@")
	_, gofmtErr := goformat.Source([]byte(n.Expression.Value))
	if gofmtErr != nil || rewritten != written || !slices.Equal(verbatimTokens(written), verbatimTokens(n.Expression.Value)) {
		// The call is kept as written if Write would change it on every
		// pass or change what it renders. Write indents each following
		// line further when gofmt cannot format the expression, and gofmt
		// is not always a fixed point (it can drop a trailing comma's
		// newline only on its second pass). Write also drops the spaces of
		// blank lines, even those inside raw strings, which are kept in the
		// generated code.
		call = linesDoc("@"+n.Expression.Value, doc.LiteralLine())
	}
	if len(n.Children) == 0 {
		if !hasChildrenBlock(n) {
			return call
		}
		// An empty children block is kept, and kept empty, since the
		// generator renders a block holding only whitespace as children:
		// without it, a following { would be read as part of the call's
		// expression or as its children.
		return doc.Concat(call, doc.Text(" {}"))
	}
	return doc.Concat(
		call,
		doc.Text(" {"),
		bodyDoc(n.Children, nil, depth, false),
		bodyEndDoc(n.Children, nil, depth),
		doc.Text("}"),
	)
}

// hasChildrenBlock reports whether the call n was written with a children
// block, which extends its range past its expression.
func hasChildrenBlock(n *parser.TemplElementExpression) bool {
	return n.Range.To.Index > n.Expression.Range.To.Index
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
	return doc.Indent(doc.HardLine(), linesGroup(nonWhitespace(nodes), nodesDoc(nodes, s, depth+1)))
}

// linesGroup is d, the layout of the sibling nodes children on lines of
// their own (a template's or a statement's body). When no child goes on its
// own line, d is grouped, so a run of inline siblings that fits stays on
// one line; otherwise every boundary between them breaks.
func linesGroup(children []parser.Node, d doc.Doc) doc.Doc {
	if hasBlockChild(children) {
		return d
	}
	return doc.Group(d)
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
// following line indented to the enclosing level plus its indentation
// relative to the other following lines, except for lines inside a Go raw
// string literal or comment, which are kept verbatim. The relative
// indentation is kept, rather than the absolute, so that printing again does
// not indent the lines further.
func codeDoc(write func(w io.Writer, indent int) error) doc.Doc {
	s := writeString(write, 0)
	literal := verbatimNewlines(s)
	if strings.HasSuffix(s, "\n }") && !literal[len(s)-3] {
		// The padding before the closing brace of { expression }, on a
		// line of its own after an expression ending in a newline, is not
		// Go code, so it is not indentation to keep: the brace goes at the
		// enclosing level.
		return doc.Concat(codeDoc(func(w io.Writer, _ int) error {
			_, err := io.WriteString(w, s[:len(s)-len("\n }")])
			return err
		}), doc.HardLine(), doc.Text("}"))
	}
	strip := commonIndent(s, literal)
	var parts []doc.Doc
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		parts = append(parts, doc.Text(s[start:i]))
		start = i + 1
		if literal[i] {
			parts = append(parts, doc.LiteralLine())
			continue
		}
		parts = append(parts, doc.HardLine())
		if strings.HasPrefix(s[start:], strip) {
			start += len(strip)
		}
	}
	return doc.Concat(append(parts, doc.Text(s[start:]))...)
}

// commonIndent is the longest run of spaces and tabs that starts every
// non-blank line of s that follows a newline not in literal.
func commonIndent(s string, literal map[int]bool) string {
	var common string
	first := true
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' || literal[i] {
			continue
		}
		line := s[i+1:]
		if end := strings.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		if strings.TrimLeft(line, " \t") == "" {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if first {
			common, first = indent, false
			continue
		}
		n := 0
		for n < len(common) && n < len(indent) && common[n] == indent[n] {
			n++
		}
		common = common[:n]
	}
	return common
}

// verbatimTokens returns the raw string literals and comments of s, which
// formatting Go code keeps as written. If s cannot be scanned as Go, it
// returns s.
func verbatimTokens(s string) []string {
	var tokens []string
	failed := false
	fset := token.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile("", fset.Base(), len(s)), []byte(s), func(token.Position, string) { failed = true }, scanner.ScanComments)
	for !failed {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			return tokens
		}
		if tok == token.COMMENT || tok == token.STRING && strings.HasPrefix(lit, "`") {
			tokens = append(tokens, lit)
		}
	}
	return []string{s}
}

// verbatimNewlines returns the offsets of the newlines in s that are inside
// a Go raw string literal or comment. If s cannot be scanned as Go, it
// returns every newline, so that all lines are kept verbatim.
func verbatimNewlines(s string) map[int]bool {
	newlines := map[int]bool{}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(s))
	failed := false
	var sc scanner.Scanner
	sc.Init(file, []byte(s), func(token.Position, string) { failed = true }, scanner.ScanComments)
	for !failed {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT && (tok != token.STRING || !strings.HasPrefix(lit, "`")) {
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
	// The parser reads a < that no element name follows as an element with
	// no name whose opening tag is the < alone, and renders it as <>. Its
	// opening tag is printed as that lone <, because <> would parse as such
	// an element followed by the text >.
	tagEnd := ">"
	if e.Name == "" {
		tagEnd = ""
	}
	if len(e.Attributes) > 0 {
		open = append(open, attributesDoc(e.Attributes)...)
	} else if gluedBefore && e.Name != "" {
		open = append(open, doc.SoftLine())
	}
	children := nonWhitespace(e.Children)
	if len(children) == 0 {
		if e.IsVoidElement() {
			return doc.Group(append(open, doc.Text("/>"))...)
		}
		return doc.Group(append(open, doc.Text(tagEnd+"</"+e.Name+">"))...)
	}
	// The opening tag is its own group, so it breaks only when it does not
	// fit itself, not when the children do.
	openTag := doc.Group(append(open, doc.Text(tagEnd))...)
	childrenDoc := nodesDoc(e.Children, siblings{ctx: parser.BoundaryInElement}, depth+1)
	first, last := children[0], children[len(children)-1]
	closeTag := doc.Text("</" + e.Name + ">")
	if _, isText := first.(*parser.Text); e.Name == "" && isText && !startsWithSpace(first) {
		// Text glued to a lone < could be read as an element name, so it
		// goes on the next line, or after a space, which the parser drops,
		// if it starts with a line-start hazard.
		edge := doc.HardLine()
		if startsWithHazard(first, false) {
			edge = doc.Text(" ")
		}
		return doc.Concat(
			openTag,
			doc.Indent(edge, childrenDoc),
			closingEdge(last, doc.HardLine()),
			closeTag,
		)
	}
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
	a = trimExpressionKey(a)
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
	case *parser.ExpressionAttribute:
		return expressionAttributeDoc(a)
	}
	return codeDoc(a.Write)
}

// expressionAttributeDoc is a, written as its Write does, with exceptions
// that keep the value as the next pass reads it. Write formats a multi-line
// expression as the element of a composite literal. When the result is a
// single line, it can keep that literal's indentation, which the next pass
// trims, so a is written as its Write output would be written once parsed
// again. The parser keeps the whitespace after a
// trailing comma as part of the value, and a newline there lays the call
// around the expression in the generated code out on lines of its own, so
// when Write would add or drop a newline at an edge of the value, or when
// gofmt cannot format a value with a newline at an edge, the value is
// written as it is.
func expressionAttributeDoc(a *parser.ExpressionAttribute) doc.Doc {
	written := writeString(a.Write, 0)
	v := a.Expression.Value
	leading, trimmed, trailing := splitEdges(v)
	edgeNewline := strings.Contains(leading, "\n") || strings.Contains(trailing, "\n")
	verbatim := codeDoc(func(w io.Writer, _ int) error {
		_, err := io.WriteString(w, a.Key.String()+"={"+v+"}")
		return err
	})
	if strings.Contains(written, "\n") {
		if strings.HasSuffix(trimmed, ",") && !edgeNewline {
			return verbatim
		}
		if edgeNewline && !strings.HasPrefix(written, a.Key.String()+"={\n") {
			// gofmt could not format the value, so Write wrote it on the
			// line of the key with its edge newlines dropped.
			return verbatim
		}
		return codeDoc(a.Write)
	}
	prefix := a.Key.String() + "={ "
	if !strings.HasPrefix(written, prefix) || !strings.HasSuffix(written, " }") {
		return codeDoc(a.Write)
	}
	if edgeNewline {
		return verbatim
	}
	c := a.Copy().(*parser.ExpressionAttribute)
	c.Expression.Value = written[len(prefix) : len(written)-len(" }")]
	return codeDoc(c.Write)
}

// trimExpressionKey returns a, or a copy of it whose expression key has no
// trailing spaces or tabs. The parser keeps the spaces before the closing
// brace of a key whose expression ends with a comma, so writing the key
// with its padding would add a space on every pass.
func trimExpressionKey(a parser.Attribute) parser.Attribute {
	key := func(a parser.Attribute) *parser.AttributeKey {
		switch a := a.(type) {
		case *parser.BoolConstantAttribute:
			return &a.Key
		case *parser.ConstantAttribute:
			return &a.Key
		case *parser.BoolExpressionAttribute:
			return &a.Key
		case *parser.ExpressionAttribute:
			return &a.Key
		}
		return nil
	}
	k := key(a)
	if k == nil {
		return a
	}
	ek, ok := (*k).(parser.ExpressionAttributeKey)
	trimmed := strings.TrimRightFunc(ek.Expression.Value, func(r rune) bool { return r != '\n' && isWhitespaceRune(r) })
	if ok && trimmed != ek.Expression.Value {
		ek.Expression.Value = trimmed
		a = a.Copy()
		*key(a) = ek
	}
	return a
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
		if isBlockElement(c) || isOwnLine(c) {
			return true
		}
	}
	return false
}

// openingEdge is the break just inside an opening tag before first.
// A line must not start with a line-start hazard, so text starting with one
// stays on the line of the opening tag, glued to it.
func openingEdge(first parser.Node, line doc.Doc) doc.Doc {
	if startsWithSpace(first) || startsWithHazard(first, false) {
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
