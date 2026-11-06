package lang

import (
	"bytes"
	"os"
	"testing"

	"laserfeet/pg"
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

func TestMigrations(t *testing.T) {
	url := os.Getenv("LF_TEST_DATABASE")
	if url == "" {
		t.Skip("LF_TEST_DATABASE isn't set")
	}
	cfg, err := pg.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	pool := pg.NewPool(cfg, 2)
	pool.Exec("drop table if exists lf_mig_a, lf_mig_b, lf_migrations", nil)
	steps := []Migration{
		{"001_a.sql", "create table lf_mig_a (id int); insert into lf_mig_a values (1), (2);"},
		{"002_b.sql", "create table lf_mig_b (id int);"},
	}
	applied, err := Migrate(pool, steps)
	if err != nil || len(applied) != 2 {
		t.Fatalf("first run: %v %v", applied, err)
	}
	if applied, err := Migrate(pool, steps); err != nil || len(applied) != 0 {
		t.Fatalf("a second run must do nothing: %v %v", applied, err)
	}
	// a failing step keeps nothing of itself, and stops there
	bad := append(steps, Migration{"003_bad.sql", "insert into lf_mig_b values (1); select * from missing_table;"})
	if _, err := Migrate(pool, bad); err == nil {
		t.Fatal("a failing migration must be reported")
	}
	res, _ := pool.Exec("select count(*) from lf_mig_b", nil)
	if *res.Rows[0][0] != "0" {
		t.Fatal("the failed migration's insert must have been undone")
	}
	// changing a step that already ran is refused
	steps[0].SQL += " -- edited"
	if _, err := Migrate(pool, steps); err == nil {
		t.Fatal("a changed migration must be refused")
	}
	pool.Exec("drop table if exists lf_mig_a, lf_mig_b, lf_migrations", nil)
}
