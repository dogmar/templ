package doc

import (
	"strings"
	"testing"
)

func paragraph() Doc {
	return Group(Text("<p>"), Indent(SoftLine(), Text("hi")), SoftLine(), Text("</p>"))
}

func TestGroupFits(t *testing.T) {
	if got, want := Print(paragraph(), 100), "<p>hi</p>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGroupBreaks(t *testing.T) {
	if got, want := Print(paragraph(), 5), "<p>\n\thi\n</p>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHardLinePropagates(t *testing.T) {
	d := Group(Text("a"), Group(Text("b"), HardLine(), Text("c")), Line(), Text("d"))
	if got, want := Print(d, 100), "ab\nc\nd"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	outer := Group(Text("x"), Indent(SoftLine(), Concat(Text("y"), HardLine(), Text("z"))))
	if got, want := Print(outer, 100), "x\n\ty\n\tz"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFill(t *testing.T) {
	d := Fill(Text("a"), Line(), Text("b"), Line(), Text("c"), Line(), Text("d"))
	if got, want := Print(d, 3), "a b\nc d"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := Print(d, 100), "a b c d"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFillSeparatorBreaksOnlyWhenNextContentDoesNotFit(t *testing.T) {
	d := Fill(Text("aaa"), Line(), Text("b"), Line(), Text("cccccc"), Line(), Text("d"))
	if got, want := Print(d, 7), "aaa b\ncccccc\nd"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTabCountsAsTabWidth(t *testing.T) {
	d := Indent(HardLine(), Group(Text("12"), Line(), Text("3456")))
	if got, want := Print(d, 10), "\n\t12\n\t3456"; got != want {
		t.Errorf("width 10: got %q, want %q", got, want)
	}
	if got, want := Print(d, 11), "\n\t12 3456"; got != want {
		t.Errorf("width 11: got %q, want %q", got, want)
	}
}

func TestNoTrailingWhitespace(t *testing.T) {
	d := Group(Text("a "), Line(), Text("b"), HardLine(), Indent(HardLine(), Text("c"), HardLine()))
	got := Print(d, 100)
	for _, l := range strings.Split(got, "\n") {
		if l != strings.TrimRight(l, " \t") {
			t.Errorf("trailing whitespace in line %q of %q", l, got)
		}
	}
	if got, want := Print(Group(Text("a"), Line(), Text("b")), 2), "a\nb"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBlankLine(t *testing.T) {
	d := Concat(Text("a"), Indent(BlankLine(), Text("b")))
	if got, want := Print(d, 100), "a\n\n\tb"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTextWithNewlinePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	Text("a\nb")
}

func TestRestCommandsAffectFit(t *testing.T) {
	d := Concat(Group(Text("ab"), Line(), Text("cd")), Text("ef"))
	if got, want := Print(d, 6), "ab\ncdef"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHardLineInFlatFillRemeasuresFollowingGroup(t *testing.T) {
	d := Fill(Concat(Text("a"), HardLine(), Group(Text("xxxx"), Line(), Text("yyyy"))), Line(), Text("z"))
	if got, want := Print(d, 6), "a\nxxxx\nyyyy z"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHardLineInFillContentBreaksEnclosingGroup(t *testing.T) {
	d := Group(Text("a"), Line(), Fill(Concat(Text("b"), HardLine(), Text("c")), Line(), Text("d")))
	if got, want := Print(d, 100), "a\nb\nc d"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLiteralLineKeepsTextVerbatim(t *testing.T) {
	d := Group(Text("<p"), Indent(Line(), Text(`x="a  `), LiteralLine(), Text(`  b"`)), SoftLine(), Text(">"))
	if got, want := Print(d, 100), "<p\n\tx=\"a  \n  b\"\n>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
