package generator_test

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/a-h/templ/internal/rendercheck"
	parser "github.com/a-h/templ/parser/v2"
)

// The boundary census renders every pair of node kinds with every gap in
// several contexts, derives the boundary classification from the rendered
// output, and checks that parser.ClassifyBoundary agrees with it.

type censusKind struct {
	name string
	src  string
	is   func(parser.Node) bool
}

func isType[T parser.Node](n parser.Node) bool {
	_, ok := n.(T)
	return ok
}

var censusKinds = []censusKind{
	{"t", "t", isType[*parser.Text]},
	{"{ s }", "{ s }", isType[*parser.StringExpression]},
	{"<b>x</b>", "<b>x</b>", isInline},
	{"<a>⏎x⏎</a>", "<a>\nx\n</a>", isInline},
	{"<div>x</div>", "<div>x</div>", isBlock},
	{"<br/>", "<br/>", isType[*parser.Element]},
	{"@c()", "@c()", isSelfClosingCall},
	{"@c() {⏎x⏎}", "@c() {\nx\n}", isCallWithChildren},
	{"if", "if x {\n<i>y</i>\n}", isType[*parser.IfExpression]},
	{"for", "for _, v := range xs {\n<i>y</i>\n}", isType[*parser.ForExpression]},
	{"switch", "switch x {\ncase 1:\n<i>y</i>\n}", isType[*parser.SwitchExpression]},
	{"<!-- c -->", "<!-- c -->", isType[*parser.HTMLComment]},
	{"// c", "// c", isType[*parser.GoComment]},
	{"{{ y := 1 }}", "{{ y := 1 }}", isType[*parser.GoCode]},
	{"{ children... }", "{ children... }", isType[*parser.ChildrenExpression]},
}

func isInline(n parser.Node) bool {
	e, ok := n.(*parser.Element)
	return ok && !e.IsBlockElement()
}

func isBlock(n parser.Node) bool {
	e, ok := n.(*parser.Element)
	return ok && e.IsBlockElement()
}

func isSelfClosingCall(n parser.Node) bool {
	e, ok := n.(*parser.TemplElementExpression)
	return ok && len(e.Children) == 0
}

func isCallWithChildren(n parser.Node) bool {
	e, ok := n.(*parser.TemplElementExpression)
	return ok && len(e.Children) > 0
}

var censusGaps = []string{"", " ", "   ", "\n", "\n\n"}

// censusContext builds the template body for a pair and a gap, and finds the
// nodes either side of the gap in the parsed body. A nil prev or next kind
// means the edge of an element.
type censusContext struct {
	name  string
	body  func(prev, next *censusKind, gap string) string
	nodes func(p *parser.Element) (container []parser.Node, prev, next parser.Node, ok bool)
	// pairs lists the (prev, next) kind pairs used by this context.
	pairs func() [][2]*censusKind
}

func allPairs() (pairs [][2]*censusKind) {
	for i := range censusKinds {
		for j := range censusKinds {
			pairs = append(pairs, [2]*censusKind{&censusKinds[i], &censusKinds[j]})
		}
	}
	return pairs
}

func nonWhitespace(nodes []parser.Node) (output []parser.Node) {
	for _, n := range nodes {
		if !isType[*parser.Whitespace](n) {
			output = append(output, n)
		}
	}
	return output
}

var censusContexts = []censusContext{
	{
		name: "siblings",
		body: func(prev, next *censusKind, gap string) string {
			return "<p>" + prev.src + gap + next.src + "\n</p>"
		},
		nodes: func(p *parser.Element) ([]parser.Node, parser.Node, parser.Node, bool) {
			nw := nonWhitespace(p.Children)
			if len(nw) != 2 {
				return nil, nil, nil, false
			}
			return p.Children, nw[0], nw[1], true
		},
		pairs: allPairs,
	},
	{
		name: "openingEdge",
		body: func(_, next *censusKind, gap string) string {
			return "<p>" + gap + next.src + "\n</p>"
		},
		nodes: func(p *parser.Element) ([]parser.Node, parser.Node, parser.Node, bool) {
			nw := nonWhitespace(p.Children)
			if len(nw) != 1 {
				return nil, nil, nil, false
			}
			return p.Children, nil, nw[0], true
		},
		pairs: func() (pairs [][2]*censusKind) {
			for i := range censusKinds {
				pairs = append(pairs, [2]*censusKind{nil, &censusKinds[i]})
			}
			return pairs
		},
	},
	{
		name: "closingEdge",
		body: func(prev, _ *censusKind, gap string) string {
			return "<p>" + prev.src + gap + "</p>"
		},
		nodes: func(p *parser.Element) ([]parser.Node, parser.Node, parser.Node, bool) {
			nw := nonWhitespace(p.Children)
			if len(nw) != 1 {
				return nil, nil, nil, false
			}
			return p.Children, nw[0], nil, true
		},
		pairs: func() (pairs [][2]*censusKind) {
			for i := range censusKinds {
				pairs = append(pairs, [2]*censusKind{&censusKinds[i], nil})
			}
			return pairs
		},
	},
	{
		// prev is the last child of an if body; next is the node after the if.
		name: "statementBodyEnd",
		body: func(prev, next *censusKind, gap string) string {
			return "<p>if x {\n" + prev.src + gap + "}\n" + next.src + "\n</p>"
		},
		nodes: func(p *parser.Element) ([]parser.Node, parser.Node, parser.Node, bool) {
			nw := nonWhitespace(p.Children)
			if len(nw) != 2 {
				return nil, nil, nil, false
			}
			ie, ok := nw[0].(*parser.IfExpression)
			if !ok {
				return nil, nil, nil, false
			}
			then := nonWhitespace(ie.Then)
			if len(then) != 1 {
				return nil, nil, nil, false
			}
			return ie.Then, then[0], nw[1], true
		},
		pairs: allPairs,
	},
	{
		// Siblings inside an if body, where the generator only strips the
		// body's leading and trailing whitespace nodes.
		name: "statementBodySiblings",
		body: func(prev, next *censusKind, gap string) string {
			return "<p>if x {\n" + prev.src + gap + next.src + "\n}\n</p>"
		},
		nodes: func(p *parser.Element) ([]parser.Node, parser.Node, parser.Node, bool) {
			nw := nonWhitespace(p.Children)
			if len(nw) != 1 {
				return nil, nil, nil, false
			}
			ie, ok := nw[0].(*parser.IfExpression)
			if !ok {
				return nil, nil, nil, false
			}
			then := nonWhitespace(ie.Then)
			if len(then) != 2 {
				return nil, nil, nil, false
			}
			return ie.Then, then[0], then[1], true
		},
		pairs: allPairs,
	},
}

const censusTemplate = "package x\n\ntempl T(x bool, xs []string, s string) {\n%s\n}\n"

// censusGap is the outcome of rendering one pair with one gap.
type censusGap struct {
	gap        string
	src        string
	valid      bool
	reason     string
	rendered   string
	prev, next parser.Node
	// whitespaceNode is true if a *parser.Whitespace node sits in the gap.
	whitespaceNode bool
}

func kindName(k *censusKind) string {
	if k == nil {
		return "(edge)"
	}
	return k.name
}

func renderCensusGap(ctx censusContext, prev, next *censusKind, gap string) (cg censusGap) {
	cg.gap = gap
	cg.src = ctx.body(prev, next, gap)
	tf, err := parser.ParseString(fmt.Sprintf(censusTemplate, cg.src))
	if err != nil {
		cg.reason = "parse error: " + err.Error()
		return cg
	}
	var p *parser.Element
	if len(tf.Nodes) == 1 {
		if ht, ok := tf.Nodes[0].(*parser.HTMLTemplate); ok {
			if children := nonWhitespace(ht.Children); len(children) == 1 {
				p, _ = children[0].(*parser.Element)
			}
		}
	}
	if p == nil || p.Name != "p" {
		cg.reason = "unexpected template shape"
		return cg
	}
	container, pn, nn, ok := ctx.nodes(p)
	if !ok || (prev != nil && !prev.is(pn)) || (next != nil && !next.is(nn)) {
		cg.reason = "pair did not parse as two separate nodes"
		return cg
	}
	if t, ok := pn.(*parser.Text); ok && strings.TrimRight(t.Value, " \t\n") != t.Value {
		cg.reason = "gap absorbed into the text value"
		return cg
	}
	if t, ok := nn.(*parser.Text); ok && strings.TrimLeft(t.Value, " \t\n") != t.Value {
		cg.reason = "gap absorbed into the text value"
		return cg
	}
	cg.prev, cg.next = pn, nn
	cg.whitespaceNode = whitespaceNodeBetween(container, pn, nn)
	if cg.rendered, err = rendercheck.Render(tf); err != nil {
		cg.reason = "render error: " + err.Error()
		return cg
	}
	cg.valid = true
	return cg
}

func whitespaceNodeBetween(container []parser.Node, prev, next parser.Node) bool {
	inGap := prev == nil
	for _, n := range container {
		switch {
		case n == prev:
			inGap = true
		case n == next:
			return false
		case inGap && isType[*parser.Whitespace](n):
			return true
		}
	}
	return false
}

var writeStringRegexp = regexp.MustCompile(`templruntime\.WriteString\(templ_7745c5c3_Buffer, \d+, ("(?:[^"\\]|\\.)*")\)`)

// staticHTML returns the static HTML written by generated code, with each
// dynamic part shown as {…}.
func staticHTML(rendered string) string {
	var sb strings.Builder
	for _, m := range writeStringRegexp.FindAllStringSubmatch(rendered, -1) {
		s, err := strconv.Unquote(m[1])
		if err != nil {
			s = m[1]
		}
		sb.WriteString(s)
	}
	return sb.String()
}

func isHorizontal(gap string) bool { return gap != "" && !strings.Contains(gap, "\n") }
func isVertical(gap string) bool   { return strings.Contains(gap, "\n") }

// classifyFromRenders derives the boundary classifications consistent with
// the rendered output of each gap. Glued and soft are reported as soft; the
// gap picks between them. If no space, or no newline, gap parses as a
// boundary, fixed cannot be ruled out and is included.
func classifyFromRenders(gaps []censusGap) (candidates []parser.Boundary, reason string) {
	var empty *censusGap
	var horizontal, vertical []string
	for i := range gaps {
		g := &gaps[i]
		if !g.valid {
			continue
		}
		switch {
		case g.gap == "":
			empty = g
		case isHorizontal(g.gap):
			horizontal = append(horizontal, g.rendered)
		case isVertical(g.gap):
			vertical = append(vertical, g.rendered)
		}
	}
	if !allEqual(horizontal) || !allEqual(vertical) {
		return nil, "UNEXPECTED: gaps of the same kind render differently"
	}
	if len(horizontal) > 0 && len(vertical) > 0 && horizontal[0] != vertical[0] {
		return []parser.Boundary{parser.BoundaryFixed}, ""
	}
	nonEmpty := append(horizontal, vertical...)
	if empty == nil || len(nonEmpty) == 0 {
		return nil, "not enough parsable gaps to classify"
	}
	b := parser.BoundarySoft
	if nonEmpty[0] == empty.rendered {
		b = parser.BoundaryIgnored
	}
	if len(horizontal) == 0 || len(vertical) == 0 {
		return []parser.Boundary{b, parser.BoundaryFixed}, ""
	}
	return []parser.Boundary{b}, ""
}

func allEqual(ss []string) bool {
	for _, s := range ss {
		if s != ss[0] {
			return false
		}
	}
	return true
}

func expectedForGap(b parser.Boundary, ws parser.TrailingSpace) parser.Boundary {
	if b == parser.BoundarySoft && ws == parser.SpaceNone {
		return parser.BoundaryGlued
	}
	return b
}

func candidatesLabel(candidates []parser.Boundary) string {
	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = c.String()
	}
	return strings.Join(labels, "|")
}

type censusResult struct {
	context, prev, next string
	candidates          []parser.Boundary
}

func TestBoundaryCensus(t *testing.T) {
	results := map[string]censusResult{}
	key := func(ctx, prev, next string) string { return ctx + "|" + prev + "|" + next }
	var skipped, inexpressible []string
	for _, ctx := range censusContexts {
		for _, pair := range ctx.pairs() {
			prev, next := pair[0], pair[1]
			name := fmt.Sprintf("%s: %s → %s", ctx.name, kindName(prev), kindName(next))
			gaps := make([]censusGap, len(censusGaps))
			var whitespaceNode bool
			for i, gap := range censusGaps {
				gaps[i] = renderCensusGap(ctx, prev, next, gap)
				if !gaps[i].valid {
					t.Logf("%s: gap %q skipped: %s", name, gap, gaps[i].reason)
				}
				whitespaceNode = whitespaceNode || gaps[i].whitespaceNode
			}
			candidates, reason := classifyFromRenders(gaps)
			if candidates == nil {
				if strings.HasPrefix(reason, "UNEXPECTED") {
					t.Errorf("%s: %s", name, reason)
				}
				skipped = append(skipped, fmt.Sprintf("%s (%s)", name, reason))
				continue
			}
			results[key(ctx.name, kindName(prev), kindName(next))] = censusResult{ctx.name, kindName(prev), kindName(next), candidates}
			var mismatches []string
			for _, g := range gaps {
				if !g.valid {
					continue
				}
				ws, err := parser.NewTrailingSpace(g.gap)
				if err != nil {
					t.Fatalf("invalid gap %q: %v", g.gap, err)
				}
				got := parser.ClassifyBoundary(g.prev, g.next, ws)
				var wants []string
				for _, c := range candidates {
					want := expectedForGap(c, ws)
					if got == want {
						wants = nil
						break
					}
					wants = append(wants, want.String())
				}
				if wants != nil {
					mismatches = append(mismatches, fmt.Sprintf("gap %q: rendered output says %s, ClassifyBoundary says %s (%q renders %q)",
						g.gap, strings.Join(wants, "|"), got, g.src, staticHTML(g.rendered)))
				}
			}
			if len(mismatches) == 0 {
				continue
			}
			if whitespaceNode {
				// The generator renders a *parser.Whitespace node in this
				// context, which ClassifyBoundary(prev, next, ws) cannot see.
				inexpressible = append(inexpressible, fmt.Sprintf("%s: %d of %d gaps differ, e.g. %s", name, len(mismatches), len(gaps), mismatches[0]))
				continue
			}
			for _, m := range mismatches {
				t.Errorf("%s %s", name, m)
			}
		}
	}

	anchors := []struct {
		prev, next string
		gap        parser.TrailingSpace
		want       parser.Boundary
	}{
		{"t", "@c()", parser.SpaceHorizontal, parser.BoundaryFixed},
		{"{ s }", "<a>⏎x⏎</a>", parser.SpaceNone, parser.BoundaryGlued},
		{"<b>x</b>", "<b>x</b>", parser.SpaceHorizontal, parser.BoundarySoft},
		{"<!-- c -->", "t", parser.SpaceHorizontal, parser.BoundaryIgnored},
	}
	for _, a := range anchors {
		r, ok := results[key("siblings", a.prev, a.next)]
		if !ok {
			t.Errorf("anchor %s → %s: not classified by the census", a.prev, a.next)
			continue
		}
		if len(r.candidates) != 1 || expectedForGap(r.candidates[0], a.gap) != a.want {
			t.Errorf("anchor %s → %s with gap %q: expected %s, got %s", a.prev, a.next, a.gap, a.want, candidatesLabel(r.candidates))
		}
	}

	logCensusSummary(t, results, skipped, inexpressible)
}

func logCensusSummary(t *testing.T, results map[string]censusResult, skipped, inexpressible []string) {
	counts := map[string]map[string]int{}
	var notIgnored []string
	for _, r := range results {
		label := candidatesLabel(r.candidates)
		if counts[r.context] == nil {
			counts[r.context] = map[string]int{}
		}
		counts[r.context][label]++
		if label != parser.BoundaryIgnored.String() {
			notIgnored = append(notIgnored, fmt.Sprintf("%s: %s → %s = %s", r.context, r.prev, r.next, label))
		}
	}
	sort.Strings(notIgnored)
	sort.Strings(skipped)
	var sb strings.Builder
	sb.WriteString("census summary (soft includes glued at gap \"\"; a|fixed means no space or no newline gap parsed, so fixed is not ruled out):\n")
	for _, ctx := range censusContexts {
		var labels []string
		for label, n := range counts[ctx.name] {
			labels = append(labels, fmt.Sprintf("%s=%d", label, n))
		}
		sort.Strings(labels)
		fmt.Fprintf(&sb, "  %s: %s\n", ctx.name, strings.Join(labels, " "))
	}
	fmt.Fprintf(&sb, "not ignored (%d):\n  %s\n", len(notIgnored), strings.Join(notIgnored, "\n  "))
	fmt.Fprintf(&sb, "skipped pairs (%d):\n  %s\n", len(skipped), strings.Join(skipped, "\n  "))
	fmt.Fprintf(&sb, "inexpressible (%d):\n  %s\n", len(inexpressible), strings.Join(inexpressible, "\n  "))
	t.Log(sb.String())
}
