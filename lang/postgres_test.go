package lang

import (
	"bytes"
	"os"
	"testing"
)

// Runs the inventory example's tests against a real PostgreSQL server
// (set LF_TEST_DATABASE, like postgres://user:password@127.0.0.1:5432/postgres).
func TestInventoryAppOnPostgres(t *testing.T) {
	url := os.Getenv("LF_TEST_DATABASE")
	if url == "" {
		t.Skip("LF_TEST_DATABASE isn't set")
	}
	t.Setenv("DATABASE_URL", url)
	src, err := os.ReadFile("../examples/inventory/app.lf")
	if err != nil {
		t.Fatal(err)
	}
	if problems := Check(string(src), nil); len(problems) > 0 {
		t.Fatalf("lf check: %v", problems)
	}
	results, err := NewInterp(&bytes.Buffer{}).RunTests(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 5 {
		t.Fatalf("expected the example's 5 tests, got %d", len(results))
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Msg)
		}
	}
}
