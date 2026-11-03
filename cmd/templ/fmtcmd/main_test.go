package fmtcmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ/internal/format"
	parser "github.com/a-h/templ/parser/v2"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"
)

//go:embed testdata.txtar
var testDataTxTar []byte

type testProject struct {
	dir       string
	cleanup   func() error
	testFiles map[string]testFile
}

type testFile struct {
	name            string
	input, expected string
}

func setupProjectDir() (tp testProject, err error) {
	tp.dir, err = os.MkdirTemp("", "fmtcmd_test_*")
	if err != nil {
		return tp, fmt.Errorf("failed to make test dir: %w", err)
	}
	tp.testFiles = make(map[string]testFile)
	testData := txtar.Parse(testDataTxTar)
	for i := 0; i < len(testData.Files); i += 2 {
		file := testData.Files[i]
		err = os.WriteFile(filepath.Join(tp.dir, file.Name), file.Data, 0660)
		if err != nil {
			return tp, fmt.Errorf("failed to write file: %w", err)
		}
		tp.testFiles[file.Name] = testFile{
			name:     filepath.Join(tp.dir, file.Name),
			input:    string(file.Data),
			expected: string(testData.Files[i+1].Data),
		}
	}
	tp.cleanup = func() error {
		if err := os.RemoveAll(tp.dir); err != nil {
			return fmt.Errorf("failed to remove test directory %q: %w", tp.dir, err)
		}
		return nil
	}
	return tp, nil
}

func TestFormat(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	t.Run("can format a single file from stdin to stdout", func(t *testing.T) {
		tp, err := setupProjectDir()
		if err != nil {
			t.Fatalf("failed to setup project dir: %v", err)
		}
		defer func() {
			if err := tp.cleanup(); err != nil {
				t.Errorf("cleanup error: %v", err)
			}
		}()
		stdin := strings.NewReader(tp.testFiles["a.templ"].input)
		stdout := new(strings.Builder)
		if err = Run(log, stdin, stdout, Arguments{
			ToStdout: true,
		}); err != nil {
			t.Fatalf("failed to run format command: %v", err)
		}
		if diff := cmp.Diff(tp.testFiles["a.templ"].expected, stdout.String()); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("can process a single file to stdout", func(t *testing.T) {
		tp, err := setupProjectDir()
		if err != nil {
			t.Fatalf("failed to setup project dir: %v", err)
		}
		defer func() {
			if err := tp.cleanup(); err != nil {
				t.Errorf("cleanup error: %v", err)
			}
		}()
		stdout := new(strings.Builder)
		if err = Run(log, nil, stdout, Arguments{
			ToStdout: true,
			Files: []string{
				tp.testFiles["a.templ"].name,
			},
			FailIfChanged: false,
		}); err != nil {
			t.Fatalf("failed to run format command: %v", err)
		}
		if diff := cmp.Diff(tp.testFiles["a.templ"].expected, stdout.String()); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("can process a single file in place", func(t *testing.T) {
		tp, err := setupProjectDir()
		if err != nil {
			t.Fatalf("failed to setup project dir: %v", err)
		}
		defer func() {
			if err := tp.cleanup(); err != nil {
				t.Errorf("cleanup error: %v", err)
			}
		}()
		if err = Run(log, nil, nil, Arguments{
			Files: []string{
				tp.testFiles["a.templ"].name,
			},
			FailIfChanged: false,
		}); err != nil {
			t.Fatalf("failed to run format command: %v", err)
		}
		data, err := os.ReadFile(tp.testFiles["a.templ"].name)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if diff := cmp.Diff(tp.testFiles["a.templ"].expected, string(data)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("fails when fail flag used and change occurs", func(t *testing.T) {
		tp, err := setupProjectDir()
		if err != nil {
			t.Fatalf("failed to setup project dir: %v", err)
		}
		defer func() {
			if err := tp.cleanup(); err != nil {
				t.Errorf("cleanup error: %v", err)
			}
		}()
		if err = Run(log, nil, nil, Arguments{
			Files: []string{
				tp.testFiles["a.templ"].name,
			},
			FailIfChanged: true,
		}); err == nil {
			t.Fatal("command should have exited with an error and did not")
		}
		data, err := os.ReadFile(tp.testFiles["a.templ"].name)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if diff := cmp.Diff(tp.testFiles["a.templ"].expected, string(data)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("passes when fail flag used and no change occurs", func(t *testing.T) {
		tp, err := setupProjectDir()
		if err != nil {
			t.Fatalf("failed to setup project dir: %v", err)
		}
		defer func() {
			if err := tp.cleanup(); err != nil {
				t.Errorf("cleanup error: %v", err)
			}
		}()
		if err = Run(log, nil, nil, Arguments{
			Files: []string{
				tp.testFiles["c.templ"].name,
			},
			FailIfChanged: true,
		}); err != nil {
			t.Fatalf("failed to run format command: %v", err)
		}
		data, err := os.ReadFile(tp.testFiles["c.templ"].name)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if diff := cmp.Diff(tp.testFiles["c.templ"].expected, string(data)); diff != "" {
			t.Error(diff)
		}
	})
}

func TestRunLeavesFileUntouchedOnRenderMismatch(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// The injected printer changes the rendered output of bad.templ only.
	restore := format.ReplacePrinterForTest(func(tf *parser.TemplateFile, width int) ([]byte, error) {
		out, err := format.Print(tf, width)
		if err != nil || filepath.Base(tf.Filepath) != "bad.templ" {
			return out, err
		}
		return bytes.ReplaceAll(out, []byte("Hello"), []byte("Goodbye")), nil
	})
	defer restore()

	dir := t.TempDir()
	bad := "package test\n\ntempl bad() {\n<p>Hello</p>\n}\n"
	good := "package test\n\ntempl good() {\n<p>Hello</p>\n}\n"
	goodFormatted := "package test\n\ntempl good() {\n\t<p>Hello</p>\n}\n"
	badPath, goodPath := filepath.Join(dir, "bad.templ"), filepath.Join(dir, "good.templ")
	if err := os.WriteFile(badPath, []byte(bad), 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodPath, []byte(good), 0660); err != nil {
		t.Fatal(err)
	}

	err := Run(log, nil, nil, Arguments{Files: []string{dir}})
	if err == nil || !strings.Contains(err.Error(), "formatting would change rendered output") {
		t.Fatalf("expected a render mismatch error, got %v", err)
	}
	if actual, _ := os.ReadFile(badPath); string(actual) != bad {
		t.Errorf("expected bad.templ to be untouched, got:\n%s", actual)
	}
	if actual, _ := os.ReadFile(goodPath); string(actual) != goodFormatted {
		t.Errorf("expected good.templ to be formatted, got:\n%s", actual)
	}
}

func TestRunUsesWidth(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	src := "package test\n\ntempl a() {\n\t<p>one two three four five six</p>\n}\n"
	expected := "package test\n\ntempl a() {\n\t<p>\n\t\tone two three\n\t\tfour five six\n\t</p>\n}\n"
	stdout := new(strings.Builder)
	if err := Run(log, strings.NewReader(src), stdout, Arguments{ToStdout: true, Width: 22}); err != nil {
		t.Fatalf("failed to run format command: %v", err)
	}
	if diff := cmp.Diff(expected, stdout.String()); diff != "" {
		t.Error(diff)
	}
}

func TestRunLogsStdinRenderMismatch(t *testing.T) {
	logs := new(bytes.Buffer)
	log := slog.New(slog.NewJSONHandler(logs, nil))
	restore := format.ReplacePrinterForTest(func(tf *parser.TemplateFile, width int) ([]byte, error) {
		out, err := format.Print(tf, width)
		return bytes.ReplaceAll(out, []byte("Hello"), []byte("Goodbye")), err
	})
	defer restore()

	stdout := new(strings.Builder)
	err := Run(log, strings.NewReader("package test\n\ntempl a() {\n<p>Hello</p>\n}\n"), stdout, Arguments{ToStdout: true})
	if err == nil || !strings.Contains(err.Error(), "stdin: formatting would change rendered output") {
		t.Fatalf("expected a render mismatch error naming stdin, got %v", err)
	}
	if !strings.Contains(logs.String(), "formatting would change rendered output") {
		t.Errorf("expected the error to be logged, got logs: %s", logs.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("expected nothing on stdout, got %q", stdout.String())
	}
}
