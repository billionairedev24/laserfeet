package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixRewritesOldSyntax(t *testing.T) {
	old := `use web
use postgres
db = postgres.open("postgres://x@localhost/db")
server = web.open(8080)
server.get("/u/:id") {
    send request.params.id
}
beam move() {
    db.transaction() {
        tx.run("select 1")
    }
}
show "n: " + [1, 2] then count
`
	want := `use web
use postgres
db = postgres.open("postgres://x@localhost/db")
server = web.open(8080)
server.get("/u/:id") as request {
    send request.params.id
}
beam move() {
    transaction with db as tx {
        tx.run("select 1")
    }
}
show ("n: " + [1, 2]) then count
`
	got, n, err := fixSource(old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if n != 4 {
		t.Errorf("expected 4 changes, got %d", n)
	}
	// fixing again changes nothing
	if again, n, _ := fixSource(got, nil); again != got || n != 0 {
		t.Errorf("a second lf fix changed things: %d changes", n)
	}
}

func TestFmtCheckCommand(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "lf")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "app.lf")
	os.WriteFile(f, []byte("launch {\nshow 1+2\n}\n"), 0o644)
	if out, err := exec.Command(bin, "fmt", "--check", dir).CombinedOutput(); err == nil || !strings.Contains(string(out), "is not formatted") {
		t.Fatalf("--check should fail on unformatted code: %v %s", err, out)
	}
	if out, err := exec.Command(bin, "fmt", dir).CombinedOutput(); err != nil {
		t.Fatalf("fmt: %v %s", err, out)
	}
	b, _ := os.ReadFile(f)
	if string(b) != "launch {\n    show 1 + 2\n}\n" {
		t.Fatalf("formatted file is %q", b)
	}
	if out, err := exec.Command(bin, "fmt", "--check", dir).CombinedOutput(); err != nil {
		t.Fatalf("--check should pass now: %v %s", err, out)
	}
}
