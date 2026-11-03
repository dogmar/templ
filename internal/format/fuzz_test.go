package format

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/a-h/templ/internal/rendercheck"
	"golang.org/x/tools/txtar"
)

// noPrettier names a command that is not installed, so that Prettier is
// skipped, keeping the fuzzer fast and deterministic.
const noPrettier = "templ-fuzz-no-such-prettier-command"

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
		out, _, err := Templ(src, "", config)
		if mismatch := (*rendercheck.MismatchError)(nil); errors.As(err, &mismatch) {
			t.Fatalf("formatting would change rendered output: %v", err)
		}
		if err != nil {
			t.Skip()
		}
		out2, changed, err := Templ(out, "", config)
		if err != nil {
			t.Fatalf("second pass failed: %v\n%s", err, out)
		}
		if changed {
			t.Fatalf("not idempotent:\n%s\n--- vs ---\n%s", out, out2)
		}
	})
}
