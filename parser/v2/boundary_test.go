package parser

import (
	"fmt"
	"testing"
)

// The old generator logic, copied from generator.go before it moved to
// RendersTrailingSpace. It is the reference truth table.

func oldIsInlineOrText(next Node) bool {
	if next == nil {
		return false
	}
	switch n := next.(type) {
	case *IfExpression:
		return true
	case *SwitchExpression:
		return true
	case *ForExpression:
		return true
	case *Element:
		return !n.IsBlockElement()
	case *Text:
		return true
	case *StringExpression:
		return true
	}
	return false
}

func oldIsTextLike(n Node) bool {
	switch n.(type) {
	case *Text:
		return true
	case *StringExpression:
		return true
	}
	return false
}

func oldIsSelfClosingTemplElementExpression(n Node) bool {
	tee, ok := n.(*TemplElementExpression)
	return ok && len(tee.Children) == 0
}

func oldIsTrailingSpaceNeeded(current, next Node) bool {
	if oldIsInlineOrText(current) && oldIsInlineOrText(next) {
		return true
	}
	if oldIsSelfClosingTemplElementExpression(current) && oldIsTextLike(next) {
		return true
	}
	if oldIsTextLike(current) && oldIsSelfClosingTemplElementExpression(next) {
		ws, ok := current.(WhitespaceTrailer)
		return ok && ws.Trailing() == SpaceHorizontal
	}
	return false
}

var trailingSpaces = []TrailingSpace{SpaceNone, SpaceHorizontal, SpaceVertical}

// boundaryTestNodes returns one node of every kind the generator writes, with
// every trailing space for the kinds that carry one.
func boundaryTestNodes() (nodes []Node) {
	for _, ts := range trailingSpaces {
		nodes = append(nodes,
			&Text{Value: "t", TrailingSpace: ts},
			&StringExpression{TrailingSpace: ts},
			&Element{Name: "b", TrailingSpace: ts},
			&Element{Name: "div", TrailingSpace: ts},
			&Element{Name: "br", TrailingSpace: ts},
			&TemplElementExpression{TrailingSpace: ts},
			&TemplElementExpression{TrailingSpace: ts, Children: []Node{&Text{Value: "x"}}},
			&GoCode{TrailingSpace: ts},
		)
	}
	return append(nodes,
		nil,
		&IfExpression{},
		&ForExpression{},
		&SwitchExpression{},
		&HTMLComment{},
		&GoComment{},
		&ChildrenExpression{},
		&CallTemplateExpression{},
		&RawElement{Name: "style"},
		&ScriptElement{},
		&DocType{},
		&Fallthrough{},
	)
}

func describeNode(n Node) string {
	if n == nil {
		return "nil"
	}
	desc := fmt.Sprintf("%T", n)
	switch n := n.(type) {
	case *Element:
		desc += " " + n.Name
	case *TemplElementExpression:
		desc += fmt.Sprintf(" children=%d", len(n.Children))
	}
	if ws, ok := n.(WhitespaceTrailer); ok {
		desc += fmt.Sprintf(" trailing=%q", ws.Trailing())
	}
	return desc
}

func TestClassifyBoundaryMatchesGenerator(t *testing.T) {
	nodes := boundaryTestNodes()
	t.Run("RendersTrailingSpace matches the old generator logic", func(t *testing.T) {
		for _, current := range nodes {
			for _, next := range nodes {
				want := oldIsTrailingSpaceNeeded(current, next)
				if got := RendersTrailingSpace(current, next); got != want {
					t.Errorf("RendersTrailingSpace(%s, %s): expected %v, got %v", describeNode(current), describeNode(next), want, got)
				}
			}
		}
	})
	t.Run("ClassifyBoundary agrees with the old generator logic", func(t *testing.T) {
		for _, prev := range nodes {
			for _, next := range nodes {
				for _, ws := range trailingSpaces {
					got := ClassifyBoundary(prev, next, ws)
					want := expectedBoundary(prev, next, ws)
					if got != want {
						t.Errorf("ClassifyBoundary(%s, %s, %q): expected %v, got %v", describeNode(prev), describeNode(next), ws, want, got)
					}
				}
			}
		}
	})
}

// expectedBoundary derives the boundary from what the old generator logic
// renders for a space and for a newline after prev.
func expectedBoundary(prev, next Node, ws TrailingSpace) Boundary {
	if _, ok := prev.(WhitespaceTrailer); !ok {
		return BoundaryIgnored
	}
	horizontal := oldIsTrailingSpaceNeeded(withTrailing(prev, SpaceHorizontal), next)
	vertical := oldIsTrailingSpaceNeeded(withTrailing(prev, SpaceVertical), next)
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

func withTrailing(n Node, ts TrailingSpace) Node {
	switch n := n.(type) {
	case *Text:
		c := *n
		c.TrailingSpace = ts
		return &c
	case *StringExpression:
		c := *n
		c.TrailingSpace = ts
		return &c
	case *Element:
		c := *n
		c.TrailingSpace = ts
		return &c
	case *TemplElementExpression:
		c := *n
		c.TrailingSpace = ts
		return &c
	case *GoCode:
		c := *n
		c.TrailingSpace = ts
		return &c
	}
	return n
}

func TestClassifyBoundary(t *testing.T) {
	tests := []struct {
		name       string
		prev, next Node
		ws         TrailingSpace
		expected   Boundary
	}{
		{"inline elements with a space are soft", &Element{Name: "b"}, &Element{Name: "i"}, SpaceHorizontal, BoundarySoft},
		{"inline elements with a newline are soft", &Element{Name: "b"}, &Element{Name: "i"}, SpaceVertical, BoundarySoft},
		{"inline elements without whitespace are glued", &Element{Name: "b"}, &Element{Name: "i"}, SpaceNone, BoundaryGlued},
		{"text before a self-closing call is fixed", &Text{Value: "t"}, &TemplElementExpression{}, SpaceHorizontal, BoundaryFixed},
		{"self-closing call before text is soft", &TemplElementExpression{}, &Text{Value: "t"}, SpaceVertical, BoundarySoft},
		{"block element is ignored", &Element{Name: "b"}, &Element{Name: "div"}, SpaceHorizontal, BoundaryIgnored},
		{"comment before text is ignored", &HTMLComment{}, &Text{Value: "t"}, SpaceHorizontal, BoundaryIgnored},
		{"closing edge is ignored", &Text{Value: "t"}, nil, SpaceVertical, BoundaryIgnored},
		{"opening edge is ignored", nil, &Text{Value: "t"}, SpaceVertical, BoundaryIgnored},
		{"if before text is ignored", &IfExpression{}, &Text{Value: "t"}, SpaceHorizontal, BoundaryIgnored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyBoundary(tt.prev, tt.next, tt.ws); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
