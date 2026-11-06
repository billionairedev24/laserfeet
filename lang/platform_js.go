//go:build js

package lang

import (
	"errors"
	"fmt"
)

var errNoFiles = errors.New("files can't be used in the browser playground")

func readFile(path string) (string, error) { return "", errNoFiles }

func fileExists(path string) bool { return false }

func listDir(path string) ([]string, error) { return nil, errNoFiles }

func getenv(name string) (string, bool) { return "", false }

func writeFileAtomic(path string, data []byte, perm uint32) error { return errNoFiles }

// startServer can't open a real port in the browser, so it lists the routes instead.
func startServer(in *Interp, s *Server, line int) {
	in.print(fmt.Sprintf("[playground] Your server is ready on port %d with these routes:", s.Port))
	for _, r := range s.routes {
		in.print("  " + r.method + " " + r.pattern)
	}
	in.print("[playground] Real web servers run on your computer with: lf run app.lf")
	in.print("[playground] Here, try server.visit(\"/path\") to test a route.")
}

// filesAvailable is false in the browser: stores keep their data in memory.
const filesAvailable = false
