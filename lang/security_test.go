//go:build !js

package lang

import (
	"bytes"
	"io"
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

// Requests carry trace and request IDs, and the metrics page is only for the
// machine itself (or whoever has METRICS_TOKEN), counting routes by pattern.
func TestTracesAndMetrics(t *testing.T) {
	in := NewInterp(io.Discard)
	if err := in.Run(`use web
server = web.open(8080)
server.metrics()
server.quiet()
server.get("/items/:id") as request {
    id = request.header("x-request-id")
    when id is nothing {
        send "none"
    }
    send id
}
`); err != nil {
		t.Fatal(err)
	}
	v, _ := in.globals.lookup("server")
	ts := httptest.NewServer(newHandler(in, v.(*Server)))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/items/7", nil)
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("X-Request-Id", "abc-123")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(body) != "abc-123" || r.Header.Get("X-Request-Id") != "abc-123" {
		t.Errorf("the request ID must reach the program and the reply: %q %q", body, r.Header.Get("X-Request-Id"))
	}
	if tp := r.Header.Get("Traceparent"); !strings.HasPrefix(tp, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || strings.Contains(tp, "00f067aa0ba902b7") {
		t.Errorf("the reply continues the caller's trace with its own span: %q", tp)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/items/8", nil)
	req.Header.Set("X-Request-Id", "bad id with spaces <script>")
	r, _ = http.DefaultClient.Do(req)
	r.Body.Close()
	if id := r.Header.Get("X-Request-Id"); strings.Contains(id, " ") || len(id) != 16 {
		t.Errorf("an unsafe request ID is replaced: %q", id)
	}

	r, _ = http.Get(ts.URL + "/metrics")
	page, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(page), `lf_requests_total{method="GET",route="/items/:id",status="200"} 2`) || !strings.Contains(string(page), "lf_request_seconds_bucket") {
		t.Errorf("metrics page: %d\n%s", r.StatusCode, page)
	}
	t.Setenv("METRICS_TOKEN", "s3cret")
	if r, _ := http.Get(ts.URL + "/metrics"); r.StatusCode != 403 {
		t.Errorf("with METRICS_TOKEN set, the page needs the token, got %d", r.StatusCode)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 200 {
		t.Errorf("the token opens the page, got %d", r.StatusCode)
	}
}
