package pg

import (
	"os"
	"strings"
	"testing"
)

// These tests need a PostgreSQL server: set LF_TEST_DATABASE, for example
// postgres://laser:secret@127.0.0.1:5432/postgres
func testURL(t *testing.T) string {
	u := os.Getenv("LF_TEST_DATABASE")
	if u == "" {
		t.Skip("LF_TEST_DATABASE isn't set")
	}
	return u
}

func s(v string) *string { return &v }

func TestQueriesAgainstARealServer(t *testing.T) {
	cfg, err := ParseURL(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Connect(cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if _, err := c.Exec("drop table if exists lf_test; create table lf_test (id serial primary key, name text not null, price numeric(10,2), ok boolean, data jsonb)", nil); err == nil {
		t.Fatal("two statements in one call must be refused (protection against SQL injection)")
	}
	for _, q := range []string{"drop table if exists lf_test", "create table lf_test (id serial primary key, name text not null unique, price numeric(10,2), ok boolean, data jsonb)"} {
		if _, err := c.Exec(q, nil); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r, err := c.Exec("insert into lf_test (name, price, ok, data) values ($1, $2, $3, $4), ($5, $6, $7, $8)",
		[]*string{s("Lamp"), s("19.99"), s("true"), s(`{"tags":["light"]}`), s("x'); drop table lf_test; --"), nil, nil, nil})
	if err != nil || r.Affected != 2 {
		t.Fatalf("insert: %v %+v", err, r)
	}
	r, err = c.Exec("select id, name, price, ok, data from lf_test order by id", nil)
	if err != nil || len(r.Rows) != 2 {
		t.Fatalf("select: %v %+v", err, r)
	}
	if *r.Rows[0][2] != "19.99" || *r.Rows[0][3] != "t" || r.Rows[1][2] != nil {
		t.Errorf("values came back wrong: %v", r.Rows)
	}
	if *r.Rows[1][1] != "x'); drop table lf_test; --" {
		t.Error("a value must be stored as text, never run as SQL")
	}
	if r.Fields[2].Type != 1700 || r.Fields[3].Type != 16 {
		t.Errorf("column types: %+v", r.Fields)
	}
	_, err = c.Exec("insert into lf_test (name) values ($1)", []*string{s("Lamp")})
	if e, ok := err.(*Error); !ok || e.Code != "23505" {
		t.Errorf("a duplicate must give a unique-violation error, got %v", err)
	}
	// the connection still works after an error
	if r, err := c.Exec("select count(*) from lf_test", nil); err != nil || *r.Rows[0][0] != "2" {
		t.Errorf("after an error: %v", err)
	}
}

func TestWrongPasswordIsRefused(t *testing.T) {
	cfg, err := ParseURL(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Password = "wrong"
	if _, err := Connect(cfg); err == nil || !strings.Contains(err.Error(), "28P01") {
		t.Errorf("a wrong password must be refused, got %v", err)
	}
}

func TestPool(t *testing.T) {
	cfg, err := ParseURL(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(cfg, 3)
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := p.Exec("select pg_sleep(0.05)", nil)
			done <- err
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if p.open > 3 {
		t.Errorf("the pool opened %d connections, more than its limit of 3", p.open)
	}
}

func TestURLs(t *testing.T) {
	c, err := ParseURL("postgres://ada@db.example.com/shop")
	if err != nil || c.SSLMode != "verify-full" || c.Port != "5432" {
		t.Errorf("remote databases must default to verify-full TLS: %+v %v", c, err)
	}
	c, _ = ParseURL("postgres://ada@localhost/shop")
	if c.SSLMode != "disable" {
		t.Errorf("a database on the same machine needs no TLS by default: %+v", c)
	}
	if _, err := ParseURL("postgres://ada@db.example.com/shop?sslmode=prefer"); err == nil {
		t.Error("sslmode=prefer can silently drop encryption, so it must be refused")
	}
	if _, err := ParseURL("mysql://x@y/z"); err == nil {
		t.Error("only postgres:// addresses")
	}
}

func TestSCRAMProofsMatchTheStandard(t *testing.T) {
	// The example from RFC 7677 (SCRAM-SHA-256), with user "user" and password "pencil".
	s := &scramState{password: "pencil", nonce: "rOprNGfwEbeRWgbNEkqO"}
	s.firstBare = "n=user,r=rOprNGfwEbeRWgbNEkqO"
	final, err := s.clientFinal("r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096")
	if err != nil {
		t.Fatal(err)
	}
	if want := "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="; final != want {
		t.Errorf("client proof\n got %s\nwant %s", final, want)
	}
	if !s.verifyServer("v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=") {
		t.Error("the standard's server signature must verify")
	}
	if s.verifyServer("v=AAAA") {
		t.Error("a wrong server signature must be refused")
	}
	// too few rounds is weak; too many lets a hostile server stall the login
	for _, i := range []string{"1", "999999999"} {
		if _, err := s.clientFinal("r=rOprNGfwEbeRWgbNEkqOxyz,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=" + i); err == nil {
			t.Errorf("i=%s must be refused", i)
		}
	}
}

func TestTLS(t *testing.T) {
	u := os.Getenv("LF_TEST_DATABASE_TLS") // a server with TLS on and a self-signed certificate
	if u == "" {
		t.Skip("LF_TEST_DATABASE_TLS isn't set")
	}
	cfg, err := ParseURL(u + "?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Connect(cfg)
	if err != nil {
		t.Fatalf("sslmode=require should connect over TLS: %v", err)
	}
	r, err := c.Exec("select ssl from pg_stat_ssl where pid = pg_backend_pid()", nil)
	if err != nil || *r.Rows[0][0] != "t" {
		t.Errorf("the connection must really be encrypted: %v", err)
	}
	c.Close()
	cfg.SSLMode = "verify-full"
	if _, err := Connect(cfg); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("verify-full must refuse a certificate it can't check, got %v", err)
	}
}
