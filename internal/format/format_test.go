package format

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ/internal/imports"
	"github.com/a-h/templ/internal/prettier"
	"github.com/a-h/templ/internal/rendercheck"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "rewrite the -- out -- section of each TestFormatting fixture with the actual output")

// needsPrettier reports whether a fixture can only pass with Prettier: it is
// named for Prettier or its attributes, or its input has script or style
// contents or style attributes, which Prettier formats.
func needsPrettier(file string, input []byte) bool {
	name := filepath.Base(file)
	if strings.HasPrefix(name, "attributes_") || strings.Contains(name, "prettier") {
		return true
	}
	for _, s := range []string{"<script", "<style", "style="} {
		if bytes.Contains(input, []byte(s)) {
			return true
		}
	}
	return false
}

func TestFormatting(t *testing.T) {
	prettierAvailable := prettier.IsAvailable(prettier.DefaultCommand())
	config := Config{PrettierRequired: prettierAvailable}
	files, _ := filepath.Glob("testdata/*.txt")
	if len(files) == 0 {
		t.Errorf("no test files found")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			a, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Files) != 2 {
				t.Fatalf("expected 2 files, got %d", len(a.Files))
			}
			if !prettierAvailable && needsPrettier(file, a.Files[0].Data) {
				t.Skip("Prettier is not available")
			}
			actual, _, err := Templ(a.Files[0].Data, "", config)
			if err != nil {
				t.Fatalf("failed to format input: %v", err)
			}
			if *update {
				a.Files[1].Data = actual
				if err := os.WriteFile(file, txtar.Format(a), 0644); err != nil {
					t.Fatal(err)
				}
			}
			expected := string(a.Files[1].Data)
			if diff := cmp.Diff(expected, string(actual)); diff != "" {
				t.Errorf("Expected:\n%s\nActual:\n%s\n", showWhitespace(expected), showWhitespace(string(actual)))

				expectedLines := strings.Split(expected, "\n")
				actualLines := strings.Split(string(actual), "\n")
				if len(expectedLines) != len(actualLines) {
					t.Errorf("Expected %d lines, got %d lines", len(expectedLines), len(actualLines))
				}
			}

			// The expected output renders as the input does, once its Go
			// imports and embedded languages have been formatted.
			in := parseTempl(t, string(a.Files[0].Data))
			if in, err = imports.Process(in); err != nil {
				t.Fatalf("failed to process imports: %v", err)
			}
			if err := ApplyPrettier(in, config); err != nil {
				t.Fatalf("failed to apply prettier: %v", err)
			}
			if err := rendercheck.Equivalent(in, parseTempl(t, expected)); err != nil {
				t.Errorf("out is not render-equivalent to in: %v", err)
			}

			secondPass, _, err := Templ(actual, "", config)
			if err != nil {
				t.Fatalf("failed to format output a second time: %v", err)
			}
			if diff := cmp.Diff(string(actual), string(secondPass)); diff != "" {
				t.Errorf("expected formatting to be idempotent, but second pass differs:\n%s", diff)
			}
		})
	}
}

func showWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", "⏎\n")
	s = strings.ReplaceAll(s, "\t", "→")
	s = strings.ReplaceAll(s, " ", "·")
	return s
}

func TestConfigLineWidth(t *testing.T) {
	for _, test := range []struct{ width, expected int }{{0, DefaultWidth}, {-5, DefaultWidth}, {40, 40}} {
		if actual := (Config{Width: test.width}).LineWidth(); actual != test.expected {
			t.Errorf("Width %d: expected LineWidth %d, got %d", test.width, test.expected, actual)
		}
	}
}

func TestTemplKeepsLastLineWithoutPackage(t *testing.T) {
	// Without a package clause, a last line with no newline is parsed as a
	// template file node rather than header, so printing it on a line of
	// its own would move it into the header.
	for _, src := range []string{"//", "// a\n/* b */"} {
		out, changed, err := Templ([]byte(src), "", Config{PrettierCommand: "templ-no-such-prettier-command"})
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", src, err)
		}
		if changed || string(out) != src {
			t.Errorf("%q: expected source unchanged, got %q", src, out)
		}
	}
}

func TestTemplKeepsLoneCarriageReturn(t *testing.T) {
	// A line break printed after the carriage return would make it part
	// of a CRLF line ending, so it would no longer render.
	src := "package A\n\ntempl A() {\n\t<div>000\r</div>\n\t000\r}\n"
	out, changed, err := Templ([]byte(src), "", Config{PrettierCommand: "templ-no-such-prettier-command"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed || string(out) != src {
		t.Errorf("expected source unchanged, got %q", out)
	}
}

func TestTemplKeepsFileInsideComment(t *testing.T) {
	// A block comment left open in the header makes the rest of the file
	// comment text, which the generated code keeps as it is.
	src := "/*\npackage A\n0/**/"
	out, changed, err := Templ([]byte(src), "", Config{PrettierCommand: "templ-no-such-prettier-command"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed || string(out) != src {
		t.Errorf("expected source unchanged, got %q", out)
	}
}
