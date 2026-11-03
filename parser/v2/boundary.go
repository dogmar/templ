package parser

import "fmt"

// Boundary classifies the gap between two sibling nodes by how the whitespace
// written in it affects the rendered output. See GLOSSARY.md.
type Boundary int

const (
	// BoundaryIgnored is a gap whose whitespace never renders.
	BoundaryIgnored Boundary = iota
	// BoundaryGlued is a gap between inline siblings with no whitespace. It
	// renders as nothing, and must never gain whitespace.
	BoundaryGlued
	// BoundarySoft is a gap between inline siblings with whitespace. It renders
	// as a single space, whether the whitespace is a space or a newline.
	BoundarySoft
	// BoundaryFixed is a gap where a space and a newline render differently, so
	// the kind of whitespace must be kept.
	BoundaryFixed
)

func (b Boundary) String() string {
	switch b {
	case BoundaryIgnored:
		return "ignored"
	case BoundaryGlued:
		return "glued"
	case BoundarySoft:
		return "soft"
	case BoundaryFixed:
		return "fixed"
	}
	return fmt.Sprintf("Boundary(%d)", int(b))
}

// BoundaryContext is the kind of node list a boundary sits in, which decides
// whether the generator renders *Whitespace nodes.
type BoundaryContext int

const (
	// BoundaryInElement is the children of an element or a template. The
	// generator drops every *Whitespace node.
	BoundaryInElement BoundaryContext = iota
	// BoundaryInBody is the body of an if, else if, else, for or switch case,
	// or the children of a templ element expression (@c() { ... }). The
	// generator drops leading and trailing *Whitespace nodes, and renders each
	// interior one as a single space.
	BoundaryInBody
)

// ClassifyBoundary classifies the gap between prev and next, where ws is the
// whitespace written in the gap and ctx is the kind of node list holding them.
//
// prev and next are the nodes either side of the gap, not *Whitespace nodes.
// prev is nil at an element's opening edge, and next is nil at an element's
// closing edge or the end of a template.
//
// When prev is the last node in a body (if, else if, else, for or switch
// case), next is the node the generator passes to that body: the statement's
// next sibling or, when the statement is itself the last node of an outer
// body, that outer body's next, recursively. The children of a templ element
// expression (@c() { ... }) end with a nil next. At a body's end, ctx is
// BoundaryInElement, because the generator drops the body's trailing
// *Whitespace node.
//
// If the statement is followed by a *Whitespace node in a body, the
// generator's next is that *Whitespace node, so the gap at the end of the
// statement's body never renders. Given the following non-whitespace node
// instead, ClassifyBoundary may return glued, soft or fixed, which is safe:
// keeping the gap's whitespace as it is never changes the rendered output.
//
// ws only picks between BoundaryGlued (SpaceNone) and BoundarySoft at
// boundaries that render.
func ClassifyBoundary(prev, next Node, ws TrailingSpace, ctx BoundaryContext) Boundary {
	if prev == nil || next == nil {
		// Leading and trailing whitespace is never rendered.
		return BoundaryIgnored
	}
	if _, ok := prev.(WhitespaceTrailer); !ok {
		// Whitespace after prev is parsed as a *Whitespace node, which only
		// renders in a body.
		if ctx != BoundaryInBody {
			return BoundaryIgnored
		}
		switch next.(type) {
		case *ForExpression:
			// The for parser consumes all leading whitespace.
			return BoundaryIgnored
		case *ChildrenExpression:
			// The children parser consumes leading spaces, but not newlines.
			return BoundaryFixed
		}
		return gluedOrSoft(ws)
	}
	horizontal := rendersTrailingSpace(prev, next, SpaceHorizontal)
	vertical := rendersTrailingSpace(prev, next, SpaceVertical)
	switch {
	case horizontal && vertical:
		return gluedOrSoft(ws)
	case horizontal || vertical:
		return BoundaryFixed
	}
	return BoundaryIgnored
}

func gluedOrSoft(ws TrailingSpace) Boundary {
	if ws == SpaceNone {
		return BoundaryGlued
	}
	return BoundarySoft
}

// RendersTrailingSpace reports whether the generator writes the trailing
// whitespace of current when it is followed by next.
//
// Trailing space is written when both nodes are inline content (text, inline
// elements, control flow), or when a self-closing templ element expression is
// adjacent to text or a string expression. When text precedes a self-closing
// templ element expression, trailing space is only written for horizontal
// whitespace (same line), not vertical (different lines), to avoid adding
// spaces before block-level components.
func RendersTrailingSpace(current, next Node) bool {
	var ws TrailingSpace
	if wt, ok := current.(WhitespaceTrailer); ok {
		ws = wt.Trailing()
	}
	return rendersTrailingSpace(current, next, ws)
}

// rendersTrailingSpace is RendersTrailingSpace with ws in place of the
// trailing space of current.
func rendersTrailingSpace(current, next Node, ws TrailingSpace) bool {
	if isInlineOrText(current) && isInlineOrText(next) {
		return true
	}
	if isSelfClosingTemplElementExpression(current) && isTextLike(next) {
		return true
	}
	if isTextLike(current) && isSelfClosingTemplElementExpression(next) {
		return ws == SpaceHorizontal
	}
	return false
}

// isInlineOrText reports whether n is rendered without whitespace around it.
// Control flow is formatted as a block in the template, but is inline at
// runtime.
func isInlineOrText(n Node) bool {
	switch n := n.(type) {
	case *IfExpression, *SwitchExpression, *ForExpression, *Text, *StringExpression:
		return true
	case *Element:
		return !n.IsBlockElement()
	}
	return false
}

func isTextLike(n Node) bool {
	switch n.(type) {
	case *Text, *StringExpression:
		return true
	}
	return false
}

func isSelfClosingTemplElementExpression(n Node) bool {
	tee, ok := n.(*TemplElementExpression)
	return ok && len(tee.Children) == 0
}
