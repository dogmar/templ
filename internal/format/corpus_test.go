package format

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			// Prettier is covered by TestFormatting; skipping it here keeps
			// this test fast.
			err = checkFormat(src, path, Config{PrettierCommand: noPrettier})
			if errors.Is(err, errInvalidInput) {
				t.Logf("skipping: %v", err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
