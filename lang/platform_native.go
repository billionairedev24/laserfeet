//go:build !js

package lang

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// startServer serves real HTTP requests until the program is stopped.
func startServer(in *Interp, s *Server, line int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		in.gil.Lock()
		resp := s.handle(in, r.Method, r.URL.RequestURI(), string(body))
		in.gil.Unlock()
		if resp.ContentType != "" {
			w.Header().Set("Content-Type", resp.ContentType)
		}
		w.Header().Set("Server", "Laser Feet")
		w.WriteHeader(resp.Status)
		io.WriteString(w, resp.Body)
	})
	in.print(fmt.Sprintf("Laser Feet server running at http://localhost:%d", s.Port))
	in.gil.Unlock()
	err := http.ListenAndServe(fmt.Sprintf(":%d", s.Port), mux)
	in.gil.Lock()
	in.fail(line, "the web server stopped: %s", err.Error())
}
