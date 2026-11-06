package lang

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnginesAgree runs every conformance program, example and benchmark on
// the compiled engine and on the tree-walking engine, and requires the same
// output (and the same problem, for programs that fail).
func TestEnginesAgree(t *testing.T) {
	var files []string
	for _, pattern := range []string{"../conformance/*/*.lf", "../examples/*.lf", "../examples/mistakes/*.lf", "../bench/*.lf", "../examples/todo/*.lf"} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	if len(files) < 80 {
		t.Fatalf("found only %d programs", len(files))
	}
	run := func(src, dir string, compile bool) (string, string) {
		var out bytes.Buffer
		in := NewInterp(&out)
		in.NoCompile = !compile
		in.Dir = dir
		in.ReadSource = func(p string) (string, error) {
			b, err := os.ReadFile(filepath.Join(dir, p))
			return string(b), err
		}
		err := in.Run(src)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		return out.String(), msg
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		if strings.Contains(string(src), "server.start()") && !strings.Contains(f, "conformance") {
			continue // serves forever
		}
		if strings.Contains(string(src), "uuid()") || strings.Contains(string(src), "time.now()") || strings.Contains(string(src), "auth.") {
			continue // different every run
		}
		t.Run(f, func(t *testing.T) {
			dir := filepath.Dir(f)
			o1, e1 := run(string(src), dir, true)
			o2, e2 := run(string(src), dir, false)
			if o1 != o2 || e1 != e2 {
				t.Fatalf("the engines disagree\ncompiled: %q %q\ntree:     %q %q", o1, e1, o2, e2)
			}
		})
	}
}
