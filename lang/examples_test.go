package lang

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every examples/NAME.lf with an examples/NAME.out must print exactly that output.
func TestExamples(t *testing.T) {
	files, _ := filepath.Glob("../examples/*.lf")
	for _, f := range files {
		want, err := os.ReadFile(strings.TrimSuffix(f, ".lf") + ".out")
		if err != nil {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, _ := os.ReadFile(f)
			var out bytes.Buffer
			if err := NewInterp(&out).Run(string(src)); err != nil {
				t.Fatalf("unexpected problem: %v", err)
			}
			if out.String() != string(want) {
				t.Errorf("output differs\n--- got ---\n%s\n--- want ---\n%s", out.String(), want)
			}
		})
	}
}

// Every examples/mistakes/NAME.lf must fail with the friendly message in NAME.out.
func TestMistakes(t *testing.T) {
	files, _ := filepath.Glob("../examples/mistakes/*.lf")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, _ := os.ReadFile(f)
			want, _ := os.ReadFile(strings.TrimSuffix(f, ".lf") + ".out")
			err := NewInterp(&bytes.Buffer{}).Run(string(src))
			if err == nil {
				t.Fatalf("expected a problem, but the program ran fine")
			}
			if err.Error() != strings.TrimSpace(string(want)) {
				t.Errorf("message differs\n got: %s\nwant: %s", err, want)
			}
		})
	}
}

// Every examples/*_test.lf file's test blocks must pass.
func TestLaserFeetTests(t *testing.T) {
	files, _ := filepath.Glob("../examples/*_test.lf")
	for _, f := range files {
		src, _ := os.ReadFile(f)
		results, err := NewInterp(&bytes.Buffer{}).RunTests(string(src))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, r := range results {
			if !r.Passed {
				t.Errorf("%s: test %q failed: %s", filepath.Base(f), r.Name, r.Msg)
			}
		}
	}
}
