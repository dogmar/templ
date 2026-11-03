package format

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ/internal/prettier"
	parser "github.com/a-h/templ/parser/v2"
)

// createBenchmarkTemplate returns a templ file with componentCount components, each with
// constant attributes, plus a page that contains a style element and a script element.
func createBenchmarkTemplate(componentCount int) string {
	var sb strings.Builder
	sb.WriteString("package benchmark\n\n")
	for i := range componentCount {
		fmt.Fprintf(&sb, "templ C%d() {\n", i)
		fmt.Fprintf(&sb, "\t<div class=\"flex items-center p-4\" id=\"c%d\">\n", i)
		fmt.Fprintf(&sb, "\t\t<p style=\"color: red\">Component %d</p>\n", i)
		sb.WriteString("\t</div>\n}\n\n")
	}
	sb.WriteString("templ Page() {\n")
	sb.WriteString("\t<html>\n\t\t<head>\n")
	sb.WriteString("\t\t\t<style>\nbody { margin: 0; }\n</style>\n")
	sb.WriteString("\t\t\t<script>\nfunction greet(name) { console.log(\"hello \" + name) }\n</script>\n")
	sb.WriteString("\t\t</head>\n\t\t<body>\n\t\t\t@C0()\n\t\t</body>\n\t</html>\n}\n")
	return sb.String()
}

func BenchmarkTempl(b *testing.B) {
	if !prettier.IsAvailable(prettier.DefaultCommand()) {
		b.Skip("Prettier is not available, skipping formatting benchmarks")
	}
	for _, componentCount := range []int{1, 10, 50} {
		src := []byte(createBenchmarkTemplate(componentCount))
		b.Run(fmt.Sprintf("components=%d", componentCount), func(b *testing.B) {
			for b.Loop() {
				_, _, err := Templ(src, "", Config{PrettierRequired: true})
				if err != nil {
					b.Fatalf("failed to format: %v", err)
				}
			}
		})
	}
}

// printerShapes returns templates whose layout stresses the printer:
// deep nesting of inline and block elements, long glued runs, many words
// and many attributes, each scaled by n.
func printerShapes(n int) map[string]string {
	long := strings.Repeat("x", 120)
	bodies := map[string]string{
		"words":        "<p>" + strings.Repeat("word ", 20*n) + "</p>",
		"inline":       "<p>" + strings.Repeat("a<b>b</b>c ", 5*n) + "</p>",
		"nested-span":  strings.Repeat("<span>a ", n) + long + strings.Repeat("</span> b", n),
		"nested-glued": strings.Repeat("a<span class=\"c\">", n) + long + strings.Repeat("</span>b", n),
		"nested-div":   strings.Repeat("<div>"+long, n) + strings.Repeat("</div>", n),
		"attributes":   "<p" + strings.Repeat(" a=\""+long+"\"", n) + "></p>",
	}
	files := map[string]string{}
	for name, body := range bodies {
		files[name] = "package A\n\ntempl A() {\n" + body + "\n}\n"
	}
	return files
}

func TestPrintTimeIsBounded(t *testing.T) {
	// A layout that re-measures nested groups grows exponentially with
	// depth; formatting these files would then take far longer than the
	// generous limit.
	for name, src := range printerShapes(200) {
		tf := parseTempl(t, src)
		start := time.Now()
		if _, err := Print(tf, 100); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: printing took %v", name, d)
		}
	}
}

func BenchmarkPrint(b *testing.B) {
	for _, n := range []int{10, 40, 160} {
		for name, src := range printerShapes(n) {
			tf, err := parser.ParseString(src)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/n=%d", name, n), func(b *testing.B) {
				for b.Loop() {
					if _, err := Print(tf, 100); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
