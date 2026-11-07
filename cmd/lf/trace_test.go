package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A problem names the file it happened in (even a used file) and the calls
// that led there; recursion is shown once with a count.
func TestProblemTraces(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "lf")
	if out, err := exec.Command("go", "build", "-o", lf, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	write(t, filepath.Join(dir, "maths.lf"), "beam safeShare(total: Int, people: Int) gives Int {\n    send total % people\n}\n")
	write(t, filepath.Join(dir, "app.lf"), `use "maths.lf"

beam split(n: Int, depth: Int) gives Int {
    when depth is 0 {
        send maths.safeShare(n, depth)
    }
    send split(n, depth - 1)
}

launch {
    show split(10, 3)
}
`)
	out, err := exec.Command(lf, "run", filepath.Join(dir, "app.lf")).CombinedOutput()
	if err == nil {
		t.Fatal("the program should fail")
	}
	got := strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
	want := `maths.lf, line 2: you can't divide by zero
    in safeShare, called from app.lf, line 5
    in split, called from app.lf, line 7 (3 times)
    in split, called from app.lf, line 11
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
