// Package rendercheck compares the generated code of two templ files to
// verify that formatting did not change what a template renders.
package rendercheck

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"strings"

	"github.com/a-h/templ/generator"
	parser "github.com/a-h/templ/parser/v2"
)

var positionRegexp = regexp.MustCompile(`Line: \d+, Col: \d+`)

// MismatchError reports the first differing line of the normalized output.
type MismatchError struct {
	Before, After string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("rendered output differs: before %q, after %q", e.Before, e.After)
}

// Render returns the generator output for tf with source positions removed
// and Go formatting normalized.
func Render(tf *parser.TemplateFile) (string, error) {
	var buf bytes.Buffer
	if _, err := generator.Generate(tf, &buf, generator.WithSkipCodeGeneratedComment(), generator.WithFileName("x.templ")); err != nil {
		return "", fmt.Errorf("failed to generate: %w", err)
	}
	stripped := positionRegexp.ReplaceAll(buf.Bytes(), nil)
	formatted, err := format.Source(stripped)
	if err != nil {
		return "", fmt.Errorf("failed to format generated code: %w", err)
	}
	return string(formatted), nil
}

// Equivalent returns nil if before and after generate the same code, or a
// *MismatchError describing the first difference.
func Equivalent(before, after *parser.TemplateFile) error {
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
			bs = strings.TrimSpace(bl[i])
		}
		if i < len(al) {
			as = strings.TrimSpace(al[i])
		}
		if bs != as {
			return &MismatchError{Before: bs, After: as}
		}
	}
	return &MismatchError{}
}
