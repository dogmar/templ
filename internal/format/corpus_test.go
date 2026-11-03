package format

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ/internal/rendercheck"
)

// TestRepositoryTemplatesFormatSafely formats every .templ file in the
// repository in memory (never writing to disk) and checks that formatting is
// render-equivalent and idempotent.
func TestRepositoryTemplatesFormatSafely(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{".git": true, "node_modules": true, ".superpowers": true}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".templ") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk repository: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no .templ files")
	}
	for _, path := range files {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read: %v", err)
			}
			out, _, err := Templ(src, path, Config{})
			if mismatch := (*rendercheck.MismatchError)(nil); errors.As(err, &mismatch) {
				t.Fatalf("formatting would change rendered output: %v", err)
			}
			if err != nil {
				t.Logf("skipping, could not format: %v", err)
				return
			}
			out2, changed, err := Templ(out, path, Config{})
			if err != nil {
				t.Fatalf("second pass failed: %v", err)
			}
			if changed {
				t.Errorf("not idempotent: second pass changed the output:\n%s\n--- vs ---\n%s", out, out2)
			}
		})
	}
}
