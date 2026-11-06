//go:build !js

package lang

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const secureApp = `
use web
server = web.open(8080)
server.get("/hi/:name") as request {
    send "<h1>Hello {request.params.name}</h1>"
}
server.get("/page/:name") as request {
    send web.page("<h1>Hello {web.escape(request.params.name)}</h1>")
}
server.get("/boom") {
    secret = "database-password-123"
    fail "could not connect with {secret}"
}
server.post("/echo") as request {
    send count(request.body)
}
`

func newTestServer(t *testing.T) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	in := NewInterp(&log)
	if err := in.Run(secureApp); err != nil {
		t.Fatal(err)
	}
	v, _ := in.globals.lookup("server")
	return httptest.NewServer(newHandler(in, v.(*Server))), &log
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTextIsNeverServedAsHTML(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	r := get(t, ts.URL+"/hi/%3Cscript%3Ealert(1)%3C%2Fscript%3E")
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("plain text must be sent as text/plain, got %q", ct)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
}

func TestEscapeInPages(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	r := get(t, ts.URL+"/page/%3Cscript%3E")
	buf := new(bytes.Buffer)
	buf.ReadFrom(r.Body)
	if strings.Contains(buf.String(), "<script>") {
		t.Errorf("web.escape did not escape visitor input: %s", buf)
	}
}

func TestErrorsDoNotLeakDetails(t *testing.T) {
	ts, log := newTestServer(t)
	defer ts.Close()
	r := get(t, ts.URL+"/boom")
	buf := new(bytes.Buffer)
	buf.ReadFrom(r.Body)
	if r.StatusCode != 500 || strings.Contains(buf.String(), "password") {
		t.Errorf("visitor saw internal details: %d %s", r.StatusCode, buf)
	}
	if !strings.Contains(log.String(), "password") {
		t.Error("the details should still reach the server log")
	}
}

func TestLargeBodiesAreRefused(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	big := strings.NewReader(strings.Repeat("x", maxBodyBytes+1))
	r, err := http.Post(ts.URL+"/echo", "text/plain", big)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for a too-large body, got %d", r.StatusCode)
	}
}
