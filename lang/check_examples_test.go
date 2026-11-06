package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every example (except the deliberate mistakes) must pass lf check.
func TestExamplesPassCheck(t *testing.T) {
	files, _ := filepath.Glob("../examples/*.lf")
	more, _ := filepath.Glob("../examples/*/*.lf")
	bench, _ := filepath.Glob("../bench/*.lf")
	files = append(append(files, more...), bench...)
	for _, f := range files {
		if strings.Contains(f, "mistakes") {
			continue
		}
		src, _ := os.ReadFile(f)
		dir := filepath.Dir(f)
		problems := Check(string(src), func(p string) (string, error) {
			b, err := os.ReadFile(filepath.Join(dir, p))
			return string(b), err
		})
		for _, p := range problems {
			t.Errorf("%s: %v", f, p)
		}
	}
}
