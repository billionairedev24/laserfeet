//go:build !js

package lang

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Limits that protect every Laser Feet web server by default.
const (
	maxBodyBytes   = 1 << 20 // 1 MB per request body
	maxHeaderBytes = 1 << 16 // 64 KB of headers
)

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func listDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

func getenv(name string) (string, bool) { return os.LookupEnv(name) }

// writeFileAtomic writes to a temporary file in the same folder and renames it
// into place, so readers never see a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// securityHeaders are sent with every response.
var securityHeaders = map[string]string{
	"X-Content-Type-Options":     "nosniff",
	"X-Frame-Options":            "DENY",
	"Referrer-Policy":            "no-referrer",
	"Cross-Origin-Opener-Policy": "same-origin",
}

// newHandler turns a Laser Feet server into a protected HTTP handler.
func newHandler(in *Interp, s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		for k, v := range securityHeaders {
			w.Header().Set(k, v)
		}
		limit := int64(maxBodyBytes)
		if s.maxBody > 0 {
			limit = s.maxBody
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "Request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Could not read the request", http.StatusBadRequest)
			return
		}
		req := &incoming{method: r.Method, uri: r.URL.RequestURI(), body: string(body), ip: clientIP(r), header: map[string]string{}}
		for k, v := range r.Header {
			req.header[strings.ToLower(k)] = strings.Join(v, ", ")
		}
		req.header["cookie"] = strings.Join(r.Header.Values("Cookie"), "; ")
		req.header["host"] = r.Host
		in.gil.Lock()
		resp := s.handle(in, req)
		in.gil.Unlock()
		secure := secureHost(req)
		for _, h := range resp.Headers {
			v := h[1]
			if h[0] == "Set-Cookie" && secure {
				v += "; Secure"
			}
			w.Header().Add(h[0], v)
		}
		if resp.ContentType != "" {
			w.Header().Set("Content-Type", resp.ContentType)
		}
		w.WriteHeader(resp.Status)
		if r.Method != "HEAD" {
			io.WriteString(w, resp.Body)
		}
		if !s.quiet {
			logRequest(r.Method, r.URL.Path, resp.Status, time.Since(start), req.ip)
		}
	})
	return mux
}

// clientIP is the visitor's address, without the port. (Behind a proxy this is
// the proxy; trusting forwarded headers is up to the program.)
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// logRequest writes one line of JSON per request to standard error, for log tools.
func logRequest(method, path string, status int, took time.Duration, ip string) {
	fmt.Fprintln(os.Stderr, jsonString(map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339), "method": method, "path": path,
		"status": status, "ms": took.Milliseconds(), "ip": ip,
	}))
}

// startServer serves real HTTP requests until the program is stopped.
func startServer(in *Interp, s *Server, line int) {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.Port),
		Handler:           newHandler(in, s),
		ReadHeaderTimeout: 10 * time.Second, // stops slow-header (Slowloris) attacks
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	in.print(fmt.Sprintf("Laser Feet server running at http://localhost:%d", s.Port))
	// Ctrl-C or a stop signal (from Docker, say) lets requests in progress finish first
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		close(done)
	}()
	var err error
	in.outside(func() {
		err = srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			<-done
			fmt.Fprintln(os.Stderr, "Laser Feet server stopped")
			os.Exit(0)
		}
	})
	in.fail(line, "the web server stopped: %s", err.Error())
}

// filesAvailable says whether this platform can save files.
const filesAvailable = true

// fetchClient has firm limits so a slow or huge reply can't hang or flood a program.
var fetchClient = &http.Client{Timeout: 30 * time.Second}

const maxFetchBytes = 10 << 20 // 10 MB

func fetchURL(method, rawURL, body string) (*Response, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil, errors.New("only http:// and https:// addresses can be fetched")
	}
	req, err := http.NewRequest(method, rawURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LaserFeet/1 (+https://github.com/billionairedev24/laserfeet)")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFetchBytes {
		return nil, errors.New("the reply is bigger than 10 MB")
	}
	return &Response{Status: resp.StatusCode, Body: string(data), ContentType: resp.Header.Get("Content-Type")}, nil
}
