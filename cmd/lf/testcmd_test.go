package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"laserfeet/lang"
)

// lf test runs every file with tests under a folder, skipping installed
// packages, and --cover says which statements never ran.
func TestTestFoldersAndCoverage(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "lf")
	if out, err := exec.Command("go", "build", "-o", lf, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	proj := filepath.Join(dir, "proj")
	write(t, filepath.Join(proj, "money.lf"), `beam fee(amount: Int) gives Int {
    when amount > 100 {
        send amount % 7
    }
    send 1
}
`)
	write(t, filepath.Join(proj, "money_test.lf"), `use "money.lf"

test "small amounts pay the least" {
    expect money.fee(50) is 1
}
`)
	write(t, filepath.Join(proj, "sub", "more_test.lf"), "test \"sums\" {\n    expect 1 + 1 is 2\n}\n")
	write(t, filepath.Join(proj, lang.SoleDir, "example.com", "x", "pkg_test.lf"), "test \"never run\" {\n    expect no\n}\n")

	cmd := exec.Command(lf, "test", "--cover")
	cmd.Dir = proj
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lf test: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"money_test.lf\n  ✓ small amounts pay the least",
		"  ✓ sums",
		"coverage: money.lf 67% of 3 statements; not run: line 3",
		"2 files: 2 passed, 0 failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "never run") {
		t.Errorf("installed packages' tests must be left out:\n%s", got)
	}
}
