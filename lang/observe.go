package lang

// Watching a running app: request IDs and trace IDs (W3C Trace Context) on
// every request and log line, and a metrics page in Prometheus' text format.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)
	requestIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// trace is one request's place in a distributed trace.
type trace struct {
	TraceID, SpanID, Parent, RequestID string
}

// newTrace continues the caller's trace (a valid traceparent header) or
// starts one, and keeps a safe X-Request-Id or makes one.
func newTrace(traceparent, requestID string) trace {
	t := trace{SpanID: randomHex(8)}
	if m := traceparentPattern.FindStringSubmatch(strings.TrimSpace(traceparent)); m != nil && m[1] != strings.Repeat("0", 32) {
		t.TraceID, t.Parent = m[1], m[2]
	} else {
		t.TraceID = randomHex(16)
	}
	if requestIDPattern.MatchString(requestID) {
		t.RequestID = requestID
	} else {
		t.RequestID = t.TraceID[:16]
	}
	return t
}

func (t trace) header() string { return "00-" + t.TraceID + "-" + t.SpanID + "-01" }

// metrics counts what a server does. Routes are counted by their pattern
// (/shipments/:id), never by the path asked for, so the page stays small.
type metrics struct {
	mu       sync.Mutex
	requests map[[3]string]int64 // method, route, status -> count
	seconds  map[string]*histogram
	inFlight atomic.Int64
	started  time.Time
}

var bucketBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type histogram struct {
	counts []int64 // one per bound, then +Inf
	sum    float64
	n      int64
}

func newMetrics() *metrics {
	return &metrics{requests: map[[3]string]int64{}, seconds: map[string]*histogram{}, started: time.Now()}
}

func (m *metrics) record(method, route string, status int, took time.Duration) {
	if route == "" {
		route = "(none)"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[[3]string{method, route, fmt.Sprint(status)}]++
	h := m.seconds[route]
	if h == nil {
		h = &histogram{counts: make([]int64, len(bucketBounds)+1)}
		m.seconds[route] = h
	}
	s := took.Seconds()
	i := sort.SearchFloat64s(bucketBounds, s)
	h.counts[i]++
	h.sum += s
	h.n++
}

func promLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// page writes the metrics in Prometheus' text format.
func (m *metrics) page() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("# HELP lf_requests_total Requests answered, by method, route and status.\n# TYPE lf_requests_total counter\n")
	keys := make([][3]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], " ") < strings.Join(keys[j][:], " ") })
	for _, k := range keys {
		fmt.Fprintf(&b, "lf_requests_total{method=\"%s\",route=\"%s\",status=\"%s\"} %d\n", promLabel(k[0]), promLabel(k[1]), k[2], m.requests[k])
	}
	b.WriteString("# HELP lf_request_seconds How long requests took, by route.\n# TYPE lf_request_seconds histogram\n")
	routes := make([]string, 0, len(m.seconds))
	for r := range m.seconds {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	for _, r := range routes {
		h, cum := m.seconds[r], int64(0)
		for i, bound := range bucketBounds {
			cum += h.counts[i]
			fmt.Fprintf(&b, "lf_request_seconds_bucket{route=\"%s\",le=\"%g\"} %d\n", promLabel(r), bound, cum)
		}
		cum += h.counts[len(bucketBounds)]
		fmt.Fprintf(&b, "lf_request_seconds_bucket{route=\"%s\",le=\"+Inf\"} %d\n", promLabel(r), cum)
		fmt.Fprintf(&b, "lf_request_seconds_sum{route=\"%s\"} %g\nlf_request_seconds_count{route=\"%s\"} %d\n", promLabel(r), h.sum, promLabel(r), h.n)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintf(&b, "# HELP lf_requests_in_flight Requests being answered now.\n# TYPE lf_requests_in_flight gauge\nlf_requests_in_flight %d\n", m.inFlight.Load())
	fmt.Fprintf(&b, "# HELP lf_memory_bytes Memory in use by Laser Feet values.\n# TYPE lf_memory_bytes gauge\nlf_memory_bytes %d\n", mem.HeapAlloc)
	fmt.Fprintf(&b, "# HELP lf_gc_cycles_total Garbage collections so far.\n# TYPE lf_gc_cycles_total counter\nlf_gc_cycles_total %d\n", mem.NumGC)
	fmt.Fprintf(&b, "# HELP lf_goroutines Pieces of work running or waiting.\n# TYPE lf_goroutines gauge\nlf_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(&b, "# HELP lf_uptime_seconds Time since the server started.\n# TYPE lf_uptime_seconds gauge\nlf_uptime_seconds %.0f\n", time.Since(m.started).Seconds())
	return b.String()
}

// metricsAllowed: the page is for the machine itself (a monitoring agent next
// to the app), or for whoever sends the METRICS_TOKEN setting as a bearer token.
func metricsAllowed(ip, authorization string) bool {
	if tok := os.Getenv("METRICS_TOKEN"); tok != "" {
		got, ok := strings.CutPrefix(authorization, "Bearer ")
		return ok && subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
	}
	return ip == "127.0.0.1" || ip == "::1"
}
