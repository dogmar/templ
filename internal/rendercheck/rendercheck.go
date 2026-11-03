// Package rendercheck compares the generated code of two templ files to
// verify that formatting did not change what a template renders.
package rendercheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	"github.com/a-h/templ/generator"
	templparser "github.com/a-h/templ/parser/v2"
)

var positionRegexp = regexp.MustCompile(`Line: \d+, Col: \d+`)

// MismatchError reports the first differing line of the normalized output.
type MismatchError struct {
	Before, After string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("rendered output differs: before %q, after %q", e.Before, e.After)
}

// Render returns the generator output for tf with source positions and
// non-directive comments removed and Go formatting normalized.
//
// Go comments cannot change what a template renders, and gofmt rewrites
// comment text (for example a pair of backquotes in a doc comment becomes
// a curly quote), so a comment formatted on its own and the same comment
// formatted as part of the generated file can legitimately differ. Only
// comments that direct the compiler are kept: //go: directives, //line
// directives and build constraints.
func Render(tf *templparser.TemplateFile) (string, error) {
	var buf bytes.Buffer
	if _, err := generator.Generate(tf, &buf, generator.WithSkipCodeGeneratedComment(), generator.WithFileName("x.templ")); err != nil {
		return "", fmt.Errorf("failed to generate: %w", err)
	}
	stripped := positionRegexp.ReplaceAll(buf.Bytes(), nil)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", stripped, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		// A templ file without a package clause generates a Go fragment,
		// which only gofmt accepts; its comments are compared as written.
		formatted, err := format.Source(stripped)
		if err != nil {
			return "", fmt.Errorf("failed to format generated code: %w", err)
		}
		return string(formatted), nil
	}
	stripComments(file)
	var printed bytes.Buffer
	if err := format.Node(&printed, fset, file); err != nil {
		return "", fmt.Errorf("failed to format generated code: %w", err)
	}
	// Printing without the comments can leave their lines blank, so format
	// once more to collapse the blank lines.
	formatted, err := format.Source(printed.Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to format generated code: %w", err)
	}
	return string(formatted), nil
}

// stripComments removes every comment from file except those that direct
// the compiler. The printer takes its comments from file.Comments, or from
// the nodes' own Doc and Comment fields when that list is nil, so both are
// cleared.
func stripComments(file *ast.File) {
	file.Comments = directiveComments(file.Comments)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.File:
			n.Doc = nil
		case *ast.GenDecl:
			n.Doc = nil
		case *ast.FuncDecl:
			n.Doc = nil
		case *ast.ImportSpec:
			n.Doc, n.Comment = nil, nil
		case *ast.ValueSpec:
			n.Doc, n.Comment = nil, nil
		case *ast.TypeSpec:
			n.Doc, n.Comment = nil, nil
		case *ast.Field:
			n.Doc, n.Comment = nil, nil
		}
		return true
	})
}

// directiveComments returns the comment groups of groups that direct the
// compiler, with their other comments removed.
func directiveComments(groups []*ast.CommentGroup) []*ast.CommentGroup {
	var kept []*ast.CommentGroup
	for _, g := range groups {
		var comments []*ast.Comment
		for _, c := range g.List {
			if isDirective(c.Text) {
				comments = append(comments, c)
			}
		}
		if len(comments) > 0 {
			kept = append(kept, &ast.CommentGroup{List: comments})
		}
	}
	return kept
}

// isDirective reports whether the comment text is a //go: directive, a
// //line directive or a build constraint.
func isDirective(text string) bool {
	return strings.HasPrefix(text, "//go:") ||
		strings.HasPrefix(text, "//line ") ||
		strings.HasPrefix(text, "//line:") ||
		constraint.IsGoBuild(text) ||
		constraint.IsPlusBuild(text)
}

// Equivalent returns nil if before and after generate the same code, or a
// *MismatchError describing the first difference.
func Equivalent(before, after *templparser.TemplateFile) error {
	b, err := Render(before)
	if err != nil {
		return err
	}
	a, err := Render(after)
	if err != nil {
		return err
	}
	if a == b {
		return nil
	}
	bl, al := strings.Split(b, "\n"), strings.Split(a, "\n")
	for i := 0; i < max(len(bl), len(al)); i++ {
		var bs, as string
		if i < len(bl) {
			bs = bl[i]
		}
		if i < len(al) {
			as = al[i]
		}
		if bs != as {
			return &MismatchError{Before: strings.TrimSpace(bs), After: strings.TrimSpace(as)}
		}
	}
	return &MismatchError{}
}
