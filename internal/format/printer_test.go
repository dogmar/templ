package format

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/a-h/templ/internal/format/doc"
	"github.com/a-h/templ/internal/rendercheck"
	parser "github.com/a-h/templ/parser/v2"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"
)

// printerFixtureChecks are extra assertions on a fixture's expected output,
// stating the property the fixture exists to show.
var printerFixtureChecks = map[string]func(t *testing.T, out string){
	"glued_inline_siblings_use_tag_break.txt": func(t *testing.T, out string) {
		if !strings.Contains(out, "</span><span\n\t\t\tclass=\"x\"\n\t\t>") {
			t.Error("expected a tag break inside the glued <span> opening tag")
		}
	},
	"soft_boundary_becomes_newline.txt": func(t *testing.T, out string) {
		for _, s := range []string{"\t\t<b>one</b>\n", "\t\t<i>two</i>\n", "\t\t<em>three</em>\n"} {
			if !strings.Contains(out, s) {
				t.Errorf("expected %q on its own line", s)
			}
		}
	},
	"text_refills_at_width.txt": func(t *testing.T, out string) {
		checkMaxWidth(t, out, 100)
		if strings.Count(out, "\n") < 8 {
			t.Error("expected the sentence to be refilled over several lines")
		}
	},
	"text_space_runs_are_unbreakable.txt": func(t *testing.T, out string) {
		if !strings.Contains(out, "alpha   beta") {
			t.Error("expected alpha   beta to stay together")
		}
	},
	"text_does_not_break_before_hazards.txt": func(t *testing.T, out string) {
		for _, line := range strings.Split(out, "\n") {
			l := strings.TrimLeft(line, "\t")
			for _, h := range []string{"if ", "for ", "@", "//"} {
				if strings.HasPrefix(l, h) {
					t.Errorf("line starts with hazard %q: %q", h, line)
				}
			}
		}
	},
	"glued_close_tag_text_overflows.txt": func(t *testing.T, out string) {
		if !strings.Contains(out, "</a>.") {
			t.Error("expected </a>. to stay together")
		}
	},
	"attributes_break_one_per_line.txt": func(t *testing.T, out string) {
		checkMaxWidth(t, out, 100)
		if !strings.Contains(out, "\n\t\tclass=\"") || !strings.Contains(out, "\n\t>\n") {
			t.Error("expected each attribute and > on their own lines")
		}
	},
	"blank_lines_collapse_to_one.txt": func(t *testing.T, out string) {
		if strings.Contains(out, "\n\n\n") {
			t.Error("expected runs of blank lines to collapse to one")
		}
		if !strings.Contains(out, "\n\n") {
			t.Error("expected a blank line to be kept")
		}
	},
}

func checkMaxWidth(t *testing.T, out string, width int) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		n := utf8.RuneCountInString(line) + strings.Count(line, "\t")*(doc.TabWidth-1)
		if n > width {
			t.Errorf("line is %d columns, over %d: %q", n, width, line)
		}
	}
}

func TestPrint(t *testing.T) {
	files, _ := filepath.Glob("testdata/printer/*.txt")
	if len(files) == 0 {
		t.Fatal("no test files found")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			a, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Files) != 2 {
				t.Fatalf("expected 2 files, got %d", len(a.Files))
			}
			in, expected := string(a.Files[0].Data), string(a.Files[1].Data)
			inTF := parseTempl(t, in)
			actual, err := Print(inTF, 100)
			if err != nil {
				t.Fatalf("failed to print: %v", err)
			}
			if diff := cmp.Diff(expected, string(actual)); diff != "" {
				t.Errorf("Expected:\n%s\nActual:\n%s\n", showWhitespace(expected), showWhitespace(string(actual)))
			}
			outTF := parseTempl(t, expected)
			if err := rendercheck.Equivalent(parseTempl(t, in), outTF); err != nil {
				t.Errorf("out is not render-equivalent to in: %v", err)
			}
			second, err := Print(outTF, 100)
			if err != nil {
				t.Fatalf("failed to print out: %v", err)
			}
			if diff := cmp.Diff(expected, string(second)); diff != "" {
				t.Errorf("expected printing to be idempotent, second pass differs:\n%s", diff)
			}
			if check, ok := printerFixtureChecks[filepath.Base(file)]; ok {
				check(t, expected)
			}
		})
	}
}

func parseTempl(t *testing.T, src string) *parser.TemplateFile {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	return tf
}
