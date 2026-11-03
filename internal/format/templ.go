package format

import (
	"bytes"
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/a-h/templ/internal/imports"
	"github.com/a-h/templ/internal/prettier"
	"github.com/a-h/templ/internal/rendercheck"
	parser "github.com/a-h/templ/parser/v2"
	"github.com/a-h/templ/parser/v2/visitor"
)

type Config struct {
	// PrettierCommand is the command to run to format the content of script and style elements.
	// If empty, the default is "prettier --use-tabs --stdin-filepath $TEMPL_PRETTIER_FILENAME".
	PrettierCommand string
	// PrettierRequired indicates that formatting using Prettier must be applied.
	PrettierRequired bool
	// Width is the maximum line width. If zero or negative, the default is DefaultWidth.
	Width int
}

// DefaultWidth is the maximum line width used when Config.Width is zero or negative.
const DefaultWidth = 100

// LineWidth is the maximum line width: Width, or DefaultWidth if Width is
// zero or negative.
func (c Config) LineWidth() int {
	if c.Width <= 0 {
		return DefaultWidth
	}
	return c.Width
}

// printTemplate prints a template file. It is a variable so that tests can
// replace it with a printer that changes the rendered output.
var printTemplate = Print

// ReplacePrinterForTest replaces the printer used by Templ and
// PrintRenderEquivalent with p, and returns a function that restores it.
// It lets tests outside this package exercise the render-equivalence
// self-check; it is not safe for concurrent use.
func ReplacePrinterForTest(p func(t *parser.TemplateFile, width int) ([]byte, error)) (restore func()) {
	prev := printTemplate
	printTemplate = p
	return func() { printTemplate = prev }
}

// PrintRenderEquivalent prints t within width columns, then parses the
// output again and checks that it is render-equivalent to t. If it is not,
// it returns a *rendercheck.MismatchError and no output.
func PrintRenderEquivalent(t *parser.TemplateFile, width int) ([]byte, error) {
	out, err := printTemplate(t, width)
	if err != nil {
		return nil, fmt.Errorf("formatting error: %w", err)
	}
	reparsed, err := parser.ParseString(string(out))
	if err != nil {
		return nil, fmt.Errorf("formatted output does not parse: %w", err)
	}
	if err = rendercheck.Equivalent(t, reparsed); err != nil {
		return nil, err
	}
	return out, nil
}

// Templ formats templ source, returning the formatted output, whether it changed, and an error if any.
// The fileName is used for Go import processing, use an empty name if the source is not from a file.
func Templ(src []byte, fileName string, config Config) (output []byte, changed bool, err error) {
	t, err := parser.ParseString(string(src))
	if err != nil {
		return nil, false, err
	}
	if reason := KeepAsWritten(src, t); reason != "" {
		return src, false, nil
	}
	t.Filepath = fileName
	t, err = imports.Process(t)
	if err != nil {
		return nil, false, err
	}

	if err = ApplyPrettier(t, config); err != nil {
		return nil, false, err
	}

	out, err := PrintRenderEquivalent(t, config.LineWidth())
	if mismatch := (*rendercheck.MismatchError)(nil); errors.As(err, &mismatch) {
		name := fileName
		if name == "" {
			name = "stdin"
		}
		return nil, false, fmt.Errorf("%s: formatting would change rendered output: %w", name, err)
	}
	if err != nil {
		return nil, false, err
	}
	changed = !bytes.Equal(src, out)
	return out, changed, nil
}

// KeepAsWritten returns why src, parsed as t, is kept as it is rather than
// formatted, or "" if it can be formatted. Formatting such a file could
// change what it renders, so callers return it unchanged.
func KeepAsWritten(src []byte, t *parser.TemplateFile) (reason string) {
	if hasLoneCarriageReturn(src) {
		// A carriage return followed by a newline is a line ending, so
		// moving a line break to just after a lone carriage return would
		// turn it into one. Text keeps a lone carriage return as written,
		// and so must every line break next to it.
		return "the file has a carriage return that does not end a line"
	}
	if opensComment(t) {
		// A block comment opened in the header or the package clause and
		// never closed makes the rest of the file, and the generated code,
		// comment text, whose layout the generator keeps as it is.
		return "a block comment in the header or package clause is not closed"
	}
	if t.Package.Expression.Value == "" && len(t.Nodes) > 0 {
		// Without a package clause, every line ending in a newline is read
		// as header, so the nodes are the text of a last line with no
		// newline. Printed on lines of their own, they would be read as
		// header and render elsewhere.
		return "the file has no package clause"
	}
	return ""
}

func ApplyPrettier(t *parser.TemplateFile, config Config) (err error) {
	// Check to see if prettier can be run.
	if config.PrettierCommand == "" {
		config.PrettierCommand = prettier.DefaultCommand()
	}
	if !prettier.IsAvailable(config.PrettierCommand) {
		if config.PrettierRequired {
			return fmt.Errorf("prettier command %q is not available, please install it or set a different command using the -prettier-command flag", config.PrettierCommand)
		}
		// Prettier is not available, skip applying it.
		return nil
	}

	batch := newPrettierBatch(config.PrettierCommand)
	collector := visitor.New()
	// Calculate the depth of each ScriptElement and RawElement in the tree so that the formatting is properly indented.
	nodeToDepth := make(map[parser.Node]int)
	collector.HTMLTemplate = func(n *parser.HTMLTemplate) error {
		// Visit the children first to calculate their depth.
		for _, child := range n.Children {
			calculateNodeDepth(child, nodeToDepth, 1)
		}
		if err := batch.AddAttributes(n.Children); err != nil {
			return err
		}
		// Now that we have the depth of each node, we can collect them.
		for _, child := range n.Children {
			if err := child.Visit(collector); err != nil {
				return err
			}
		}
		return nil
	}
	collector.ScriptElement = func(se *parser.ScriptElement) error {
		batch.AddScriptElement(se, nodeToDepth[se])
		return nil
	}
	collector.RawElement = func(re *parser.RawElement) error {
		batch.AddStyleElement(re, nodeToDepth[re])
		return nil
	}
	if err = collector.VisitTemplateFile(t); err != nil {
		return err
	}

	return batch.Run()
}

func calculateNodeDepth(e parser.Node, nodeToDepth map[parser.Node]int, depth int) {
	switch e := e.(type) {
	case *parser.ScriptElement:
		nodeToDepth[e] = depth
	case *parser.RawElement:
		nodeToDepth[e] = depth
	case parser.CompositeNode:
		for _, child := range e.ChildNodes() {
			calculateNodeDepth(child, nodeToDepth, depth+1)
		}
	}
}

// opensComment reports whether the header and package clause of t, scanned
// as Go, end inside a block comment.
func opensComment(t *parser.TemplateFile) bool {
	var src strings.Builder
	for _, h := range t.Header {
		src.WriteString(h.Expression.Value)
		src.WriteString("\n")
	}
	src.WriteString(t.Package.Expression.Value)
	opens := false
	fset := token.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile("", fset.Base(), src.Len()), []byte(src.String()), func(_ token.Position, msg string) {
		opens = opens || msg == "comment not terminated"
	}, scanner.ScanComments)
	for {
		if _, tok, _ := sc.Scan(); tok == token.EOF {
			return opens
		}
	}
}

// hasLoneCarriageReturn reports whether src has a carriage return that is
// not part of a CRLF line ending.
func hasLoneCarriageReturn(src []byte) bool {
	for i, b := range src {
		if b == '\r' && (i+1 == len(src) || src[i+1] != '\n') {
			return true
		}
	}
	return false
}
