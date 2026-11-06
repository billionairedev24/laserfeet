package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestShipmentsEndToEnd runs the reference app (examples/shipments) as a real
// server and uses it over HTTP like a browser would: sign up, book a shipment,
// update it, use the JSON API, sign out. It runs twice: with lf run, and as a
// program made by lf build (which must carry its templates, files and
// migrations inside). Needs LF_TEST_DATABASE.
func TestShipmentsEndToEnd(t *testing.T) {
	dbURL := os.Getenv("LF_TEST_DATABASE")
	if dbURL == "" {
		t.Skip("LF_TEST_DATABASE isn't set")
	}
	dir := t.TempDir()
	lf := filepath.Join(dir, "lf")
	if out, err := exec.Command("go", "build", "-o", lf, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	app, _ := filepath.Abs("../../examples/shipments")
	env := append(os.Environ(), "DATABASE_URL="+dbURL, "SESSION_SECRET=an-end-to-end-test-secret-0123456789abcdef")

	migrate := exec.Command(lf, "migrate")
	migrate.Dir, migrate.Env = app, env
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("lf migrate: %v\n%s", err, out)
	}

	t.Run("lf run", func(t *testing.T) {
		cmd := exec.Command(lf, "run", filepath.Join(app, "app.lf"))
		exerciseServer(t, cmd, env)
	})
	t.Run("lf build", func(t *testing.T) {
		bin := filepath.Join(dir, "shipments")
		build := exec.Command(lf, "build", filepath.Join(app, "app.lf"), "-o", bin)
		build.Env = env
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("lf build: %v\n%s", err, out)
		}
		cmd := exec.Command(bin)
		cmd.Dir = dir // far from the source: everything must come from inside the program
		exerciseServer(t, cmd, env)
	})
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func exerciseServer(t *testing.T, cmd *exec.Cmd, env []string) {
	port := freePort(t)
	cmd.Env = append(env, fmt.Sprintf("PORT=%d", port))
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	logs := make(chan string, 1000)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			logs <- sc.Text()
		}
		close(logs)
	}()
	defer cmd.Process.Kill()
	base := fmt.Sprintf("http://localhost:%d", port)

	// wait for the server
	deadline := time.Now().Add(15 * time.Second)
	for {
		if r, err := http.Get(base + "/health"); err == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the server didn't start")
		}
		time.Sleep(100 * time.Millisecond)
	}

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	get := func(path string) (*http.Response, string) {
		r, err := browser.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, string(b)
	}
	post := func(path string, form url.Values) (*http.Response, string) {
		req, _ := http.NewRequest("POST", base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", base) // browsers send this with form posts
		r, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, string(b)
	}
	api := func(method, path string, body any) (*http.Response, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, base+path, rd)
		req.Header.Set("Content-Type", "application/json")
		r, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		json.NewDecoder(r.Body).Decode(&out)
		return r, out
	}

	// signed out: pages send you to the login page, which is a real page
	r, body := get("/shipments")
	if r.Request.URL.Path != "/login" || !strings.Contains(body, "<form") {
		t.Fatalf("expected the login page, got %s", r.Request.URL)
	}
	if r.Header.Get("X-Frame-Options") != "DENY" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers are missing")
	}
	if r, css := get("/static/site.css"); r.StatusCode != 200 || !strings.Contains(css, "--accent") || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/css") {
		t.Errorf("static file: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}

	// sign up: lands on the empty shipments page, signed in
	email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	r, body = post("/register", url.Values{"email": {email}, "password": {"a long password"}})
	if r.Request.URL.Path != "/shipments" || !strings.Contains(body, "No shipments yet") || !strings.Contains(body, email) {
		t.Fatalf("after sign-up: %s\n%s", r.Request.URL, body)
	}
	u, _ := url.Parse(base)
	cookies := jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != "lf_session" {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}

	// book a shipment with the form, then move it along
	r, body = post("/shipments", url.Values{"reference": {"E2E-1"}, "destination": {"Port Harcourt"}, "weightKg": {"7.25"}})
	if !strings.HasPrefix(r.Request.URL.Path, "/shipments/") || !strings.Contains(body, "E2E-1") || !strings.Contains(body, "7.25 kg") {
		t.Fatalf("after booking: %s\n%s", r.Request.URL, body)
	}
	page := r.Request.URL.Path
	r, body = post(page+"/status", url.Values{"status": {"InTransit"}, "carrier": {"GIG"}, "eta": {"2026-12-24"}})
	if !strings.Contains(body, "With GIG, arriving 2026-12-24") {
		t.Fatalf("after the update: %s", body)
	}

	// the JSON API sees the same shipment
	resp, list := apiList(t, browser, base)
	if resp.StatusCode != 200 || len(list) != 1 || list[0]["reference"] != "E2E-1" {
		t.Fatalf("API list: %d %v", resp.StatusCode, list)
	}
	status, _ := list[0]["status"].(map[string]any)
	if status["kind"] != "InTransit" || status["carrier"] != "GIG" {
		t.Fatalf("API status: %v", list[0]["status"])
	}
	r2, made := api("POST", "/api/shipments", map[string]any{"reference": "E2E-2", "destination": "Kano", "weightKg": 1})
	if r2.StatusCode != 201 || made["status"] != "Booked" {
		t.Fatalf("API create: %d %v", r2.StatusCode, made)
	}
	if r3, _ := api("DELETE", fmt.Sprintf("/api/shipments/%v", made["id"]), nil); r3.StatusCode != 204 {
		t.Fatalf("API delete: %d", r3.StatusCode)
	}

	// another site's form can't post as this visitor
	req, _ := http.NewRequest("POST", base+"/shipments", strings.NewReader("reference=X&destination=Y&weightKg=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	if r, err := browser.Do(req); err != nil || r.StatusCode != 403 {
		t.Fatalf("a cross-site post must be refused, got %v %v", r.StatusCode, err)
	}

	// sign out: the session is gone
	r, _ = post("/logout", nil)
	if r.Request.URL.Path != "/login" {
		t.Fatalf("after sign-out: %s", r.Request.URL)
	}
	if r, _ := get("/shipments"); r.Request.URL.Path != "/login" {
		t.Fatal("signed out, shipments must send you to the login page")
	}

	// requests are logged as JSON lines, and a stop signal shuts down cleanly
	cmd.Process.Signal(syscall.SIGTERM)
	sawLog, sawStop := false, false
	timeout := time.After(15 * time.Second)
read:
	for {
		select {
		case line, ok := <-logs:
			if !ok {
				break read // the server has exited
			}
			if strings.Contains(line, `"path":"/shipments"`) && strings.Contains(line, `"status":`) {
				sawLog = true
			}
			if strings.Contains(line, "stopped") {
				sawStop = true
			}
		case <-timeout:
			t.Fatal("the server didn't stop")
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the server should stop cleanly: %v", err)
	}
	if !sawLog || !sawStop {
		t.Errorf("expected request logs and a stop message (logs %v, stop %v)", sawLog, sawStop)
	}
}

func apiList(t *testing.T, c *http.Client, base string) (*http.Response, []map[string]any) {
	r, err := c.Get(base + "/api/shipments")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out []map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	return r, out
}
