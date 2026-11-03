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

// ClassifyBoundary classifies the gap between prev and next, where ws is the
// whitespace written in the gap.
//
// prev and next are the nodes either side of the gap, not *Whitespace nodes.
// When prev is the last node in an if, for or switch body, next is the node
// after the statement. next is nil at an element's closing edge or the end of
// a template, and prev is nil at an element's opening edge.
//
// ws only picks between BoundaryGlued (SpaceNone) and BoundarySoft at
// boundaries that render.
func ClassifyBoundary(prev, next Node, ws TrailingSpace) Boundary {
	if _, ok := prev.(WhitespaceTrailer); !ok {
		// The generator only writes whitespace that trails a node.
		return BoundaryIgnored
	}
	horizontal := rendersTrailingSpace(prev, next, SpaceHorizontal)
	vertical := rendersTrailingSpace(prev, next, SpaceVertical)
	switch {
	case horizontal && vertical && ws == SpaceNone:
		return BoundaryGlued
	case horizontal && vertical:
		return BoundarySoft
	case horizontal || vertical:
		return BoundaryFixed
	}
	return BoundaryIgnored
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
