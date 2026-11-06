//go:build !js

package lang

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
