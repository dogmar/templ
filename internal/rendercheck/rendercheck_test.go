package rendercheck

import (
	"errors"
	"strings"
	"testing"

	parser "github.com/a-h/templ/parser/v2"
)

func parse(t *testing.T, src string) *parser.TemplateFile {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	return tf
}

func wrap(body string) string {
	return "package x\n\ntempl T(a, b string) {\n" + body + "\n}\n"
}

func TestEquivalentWhitespaceAtSoftBoundary(t *testing.T) {
	before := parse(t, wrap("<p><b>a</b> <i>b</i></p>"))
	after := parse(t, wrap("<p><b>a</b>\n<i>b</i></p>"))
	if err := Equivalent(before, after); err != nil {
		t.Fatalf("expected equivalent, got %v", err)
	}
}

func TestMismatchAtGluedBoundary(t *testing.T) {
	before := parse(t, wrap("<p><span>a</span><span>b</span></p>"))
	after := parse(t, wrap("<p><span>a</span>\n<span>b</span></p>"))
	err := Equivalent(before, after)
	var me *MismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected *MismatchError, got %v", err)
	}
	if !strings.Contains(me.After, "</span> <span>") {
		t.Errorf("expected After to contain %q, got %q", "</span> <span>", me.After)
	}
}

func TestEquivalentIgnoresPositions(t *testing.T) {
	before := parse(t, wrap("<p>{ a }</p>"))
	after := parse(t, "\n\n"+wrap("<p>{ a }</p>"))
	if err := Equivalent(before, after); err != nil {
		t.Fatalf("expected equivalent, got %v", err)
	}
}

func TestEquivalentIgnoresGoSpacing(t *testing.T) {
	before := parse(t, wrap("<p>{ a+b }</p>"))
	after := parse(t, wrap("<p>{ a + b }</p>"))
	if err := Equivalent(before, after); err != nil {
		t.Fatalf("expected equivalent, got %v", err)
	}
}
