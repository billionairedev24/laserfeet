//go:build js

package lang

import (
	"errors"
	"fmt"
)

func readFile(path string) (string, error) {
	return "", errors.New("files can't be read in the browser playground")
}

// startServer can't open a real port in the browser, so it lists the routes instead.
func startServer(in *Interp, s *Server, line int) {
	in.print(fmt.Sprintf("[playground] Your server is ready on port %d with these routes:", s.Port))
	for _, r := range s.routes {
		in.print("  " + r.method + " " + r.pattern)
	}
	in.print("[playground] Real web servers run on your computer with: lf run app.lf")
	in.print("[playground] Here, try server.visit(\"/path\") to test a route.")
}
