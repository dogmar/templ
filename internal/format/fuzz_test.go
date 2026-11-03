package format

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/a-h/templ/internal/imports"
	"github.com/a-h/templ/internal/rendercheck"
	parser "github.com/a-h/templ/parser/v2"
	"golang.org/x/tools/txtar"
)

// noPrettier names a command that is not installed, so that Prettier is
// skipped, keeping the fuzzer fast and deterministic.
const noPrettier = "templ-fuzz-no-such-prettier-command"

// errInvalidInput is returned by checkFormat when the input itself cannot be
// formatted: it does not parse, its Go imports cannot be processed, or it
// does not generate valid Go.
var errInvalidInput = errors.New("invalid input")

// checkFormat formats src twice and returns an error if the formatter fails
// on it or the output is not a fixed point. An error wrapping
// errInvalidInput means src is not a template the formatter must handle;
// any other error is a formatter bug, including output that does not parse
// or generates invalid Go.
func checkFormat(src []byte, fileName string, config Config) error {
	t, err := parser.ParseString(string(src))
	if err != nil {
		return fmt.Errorf("%w: does not parse: %v", errInvalidInput, err)
	}
	if _, err := rendercheck.Render(t); err != nil {
		return fmt.Errorf("%w: does not generate: %v", errInvalidInput, err)
	}
	t.Filepath = fileName
	if t, err = imports.Process(t); err != nil {
		return fmt.Errorf("%w: imports: %v", errInvalidInput, err)
	}
	if _, err := rendercheck.Render(t); err != nil {
		return fmt.Errorf("%w: does not generate after imports: %v", errInvalidInput, err)
	}
	out, _, err := Templ(src, fileName, config)
	if err != nil {
		return fmt.Errorf("formatting failed: %w", err)
	}
	out2, changed, err := Templ(out, fileName, config)
	if err != nil {
		return fmt.Errorf("second pass failed: %w\n%s", err, out)
	}
	if changed {
		return fmt.Errorf("not idempotent:\n%s\n--- vs ---\n%s", out, out2)
	}
	return nil
}

func FuzzFormat(f *testing.F) {
	for _, dir := range []string{"testdata", filepath.Join("testdata", "printer")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range files {
			a, err := txtar.ParseFile(file)
			if err != nil {
				f.Fatalf("%s: %v", file, err)
			}
			for _, af := range a.Files {
				if af.Name == "in" {
					f.Add(af.Data)
				}
			}
		}
	}
	config := Config{PrettierCommand: noPrettier}
	f.Fuzz(func(t *testing.T, src []byte) {
		err := checkFormat(src, "", config)
		if errors.Is(err, errInvalidInput) {
			t.Skip()
		}
		if err != nil {
			t.Fatal(err)
		}
	})
}

// TestCheckFormatReportsUnparseableOutput checks that the fuzz check fails,
// rather than skips, when the printer writes output that does not parse.
func TestCheckFormatReportsUnparseableOutput(t *testing.T) {
	restore := ReplacePrinterForTest(func(*parser.TemplateFile, int) ([]byte, error) {
		return []byte("package main\n\ntempl x() {\n\t<div>\n"), nil
	})
	defer restore()
	src := []byte("package main\n\ntempl x() {\n\t<div></div>\n}\n")
	err := checkFormat(src, "", Config{PrettierCommand: noPrettier})
	if err == nil || errors.Is(err, errInvalidInput) {
		t.Fatalf("expected a formatter failure, got %v", err)
	}
	t.Log(err)
}
