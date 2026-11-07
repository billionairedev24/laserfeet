package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestReferenceAppSpeed measures the reference app (examples/shipments) the
// way it's used: a signed-in visitor's pages and the JSON API, with many
// visitors at once, against PostgreSQL. It also times the benchmarks in
// bench/. CI runs it on every push and records the numbers; it fails only if
// they fall far below the floor in bench/floor.json (shared CI machines vary
// a lot, so the floor is set well under typical results). Needs LF_PERF=1 and
// LF_TEST_DATABASE. LF_PERF_OUT names a file for the results as JSON;
// LF_PERF_BIN measures another lf program instead, to compare.
func TestReferenceAppSpeed(t *testing.T) {
	dbURL := os.Getenv("LF_TEST_DATABASE")
	if os.Getenv("LF_PERF") == "" || dbURL == "" {
		t.Skip("set LF_PERF=1 and LF_TEST_DATABASE to measure speed")
	}
	dir := t.TempDir()
	lf := filepath.Join(dir, "lf")
	if other := os.Getenv("LF_PERF_BIN"); other != "" { // measure another lf, to compare
		lf = other
	} else if out, err := exec.Command("go", "build", "-o", lf, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	results := map[string]float64{}

	// the benchmarks: best of five runs, in milliseconds
	for _, b := range []string{"fib", "loops", "parallel"} {
		best := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			if out, err := exec.Command(lf, "run", "../../bench/"+b+".lf").CombinedOutput(); err != nil {
				t.Fatalf("bench/%s.lf: %v\n%s", b, err, out)
			}
			best = min(best, time.Since(start))
		}
		results["bench_"+b+"_ms"] = float64(best.Microseconds()) / 1000
	}

	// the reference app
	app, _ := filepath.Abs("../../examples/shipments")
	env := append(os.Environ(), "DATABASE_URL="+dbURL, "SESSION_SECRET=a-speed-test-secret-0123456789abcdef0123")
	migrate := exec.Command(lf, "migrate")
	migrate.Dir, migrate.Env = app, env
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("lf migrate: %v\n%s", err, out)
	}
	port := freePort(t)
	cmd := exec.Command(lf, "run", filepath.Join(app, "app.lf"))
	cmd.Env = append(env, fmt.Sprintf("PORT=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	base := fmt.Sprintf("http://localhost:%d", port)
	for deadline := time.Now().Add(15 * time.Second); ; {
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

	// one signed-in visitor with a few shipments; every client shares the session
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{MaxIdleConnsPerHost: 64}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, Transport: transport}
	post := func(path string, form url.Values) {
		req, _ := http.NewRequest("POST", base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", base)
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}
	post("/register", url.Values{"email": {fmt.Sprintf("speed-%d@example.com", time.Now().UnixNano())}, "password": {"a long password"}})
	for i := 0; i < 5; i++ {
		post("/shipments", url.Values{"reference": {fmt.Sprintf("SPD-%d", i)}, "destination": {"Lagos"}, "weightKg": {"2.5"}})
	}

	measure := func(path string) float64 {
		var n atomic.Int64
		var failed atomic.Value
		end := time.Now().Add(4 * time.Second)
		var wg sync.WaitGroup
		for w := 0; w < 16; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for time.Now().Before(end) {
					r, err := client.Get(base + path)
					if err != nil {
						failed.Store(err.Error())
						return
					}
					io.Copy(io.Discard, r.Body)
					r.Body.Close()
					if r.StatusCode != 200 {
						failed.Store(fmt.Sprintf("%s gave %d", path, r.StatusCode))
						return
					}
					n.Add(1)
				}
			}()
		}
		wg.Wait()
		if f := failed.Load(); f != nil {
			t.Fatalf("while measuring %s: %v", path, f)
		}
		return float64(n.Load()) / 4
	}
	results["app_page_requests_per_second"] = measure("/shipments")
	results["app_api_requests_per_second"] = measure("/api/shipments")
	results["app_health_requests_per_second"] = measure("/health")

	for k, v := range results {
		t.Logf("%-34s %10.1f", k, v)
	}
	if out := os.Getenv("LF_PERF_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		os.WriteFile(out, append(b, '\n'), 0o644)
	}

	// far below normal is a problem worth stopping for
	var floor map[string]float64
	b, err := os.ReadFile("../../bench/floor.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &floor); err != nil {
		t.Fatal(err)
	}
	for k, limit := range floor {
		got, ok := results[k]
		switch {
		case !ok:
			t.Errorf("bench/floor.json names %s, which isn't measured", k)
		case strings.HasSuffix(k, "_ms") && got > limit:
			t.Errorf("%s took %.1f ms; the most allowed is %.0f", k, got, limit)
		case strings.HasSuffix(k, "_per_second") && got < limit:
			t.Errorf("%s was %.1f; the least allowed is %.0f", k, got, limit)
		}
	}
}
