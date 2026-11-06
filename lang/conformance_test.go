package lang

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConformance runs the conformance suite in ../conformance, one folder per
// section of docs/spec.md. Each NAME.lf either prints exactly NAME.out, or is
// refused (by the checker or while running) with exactly the message in NAME.err.
func TestConformance(t *testing.T) {
	files, _ := filepath.Glob("../conformance/*/*.lf")
	if len(files) < 50 {
		t.Fatalf("expected the conformance suite, found %d programs", len(files))
	}
	for _, f := range files {
		name := strings.TrimPrefix(strings.TrimSuffix(f, ".lf"), "../conformance/")
		t.Run(name, func(t *testing.T) {
			src, _ := os.ReadFile(f)
			base := strings.TrimSuffix(f, ".lf")
			got, failed := runConformance(string(src), filepath.Dir(f))
			if want, err := os.ReadFile(base + ".err"); err == nil {
				if !failed {
					t.Fatalf("expected the problem %q, but the program ran and printed:\n%s", strings.TrimSpace(string(want)), got)
				}
				if got != strings.TrimSpace(string(want)) {
					t.Fatalf("problem differs\n got: %s\nwant: %s", got, want)
				}
				return
			}
			want, err := os.ReadFile(base + ".out")
			if err != nil {
				t.Fatalf("%s needs a .out or a .err file", f)
			}
			if failed {
				t.Fatalf("expected it to run, but: %s", got)
			}
			if got != string(want) {
				t.Fatalf("output differs\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

// runConformance does what lf run does: check first, then run.
func runConformance(src, dir string) (string, bool) {
	read := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		return string(b), err
	}
	if problems := Check(src, read); len(problems) > 0 {
		return fmt.Sprintf("line %d: %s", problems[0].Line, problems[0].Msg), true
	}
	var out bytes.Buffer
	in := NewInterp(&out)
	in.ReadSource = read
	if err := in.Run(src); err != nil {
		switch e := err.(type) {
		case *RuntimeError:
			return fmt.Sprintf("line %d: %s", e.Line, e.Msg), true
		case *SyntaxError:
			return fmt.Sprintf("line %d: %s", e.Line, e.Msg), true
		}
		return err.Error(), true
	}
	return out.String(), false
}
