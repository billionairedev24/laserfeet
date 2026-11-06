//go:build !js

package lang

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		for k, v := range securityHeaders {
			w.Header().Set(k, v)
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "Request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Could not read the request", http.StatusBadRequest)
			return
		}
		in.gil.Lock()
		resp := s.handle(in, r.Method, r.URL.RequestURI(), string(body))
		in.gil.Unlock()
		if resp.ContentType != "" {
			w.Header().Set("Content-Type", resp.ContentType)
		}
		w.WriteHeader(resp.Status)
		io.WriteString(w, resp.Body)
	})
	return mux
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
	in.gil.Unlock()
	err := srv.ListenAndServe()
	in.gil.Lock()
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
