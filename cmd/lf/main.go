// Command lf runs Laser Feet programs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"laserfeet/auth"
	"laserfeet/lang"
)

var version = "dev" // set from the VERSION file by scripts/build.sh

var help = `Laser Feet ` + version + `

Usage:
  lf new <name>        create a starter project
  lf run <file.lf>     run a program
  lf test <file.lf>    run the test blocks in a file
  lf check <file.lf>   find type mistakes without running anything
  lf migrate [folder]  apply new .sql migrations to DATABASE_URL
  lf secret            print a new random secret (for server.sessions)
  lf fmt [files]       lay out code the standard way (--check: only report)
  lf fix [files]       update code written for an older Laser Feet
  lf get <package>     add a package, like: lf get github.com/someone/greetings@v1.2.0
  lf get               install the packages in sole.list, checked against sole.lock
  lf remove <package>  stop using a package
  lf lsp               start the language server for editors (VS Code uses this)
  lf build <file.lf>   make a standalone program (add -o name to pick its name)
  lf version           show the version
  lf help              show this help

You can also just type: lf <file.lf>
`

func main() {
	// A program made with "lf build" carries its code inside; run that instead.
	if code, found := runAttached(); found {
		os.Exit(code)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(help)
		return
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(help)
	case "version", "--version":
		fmt.Println("Laser Feet " + version)
	case "new":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Please give the project a name, like: lf new my-app")
			os.Exit(2)
		}
		os.Exit(newProject(args[1]))
	case "get":
		os.Exit(soleGet(args[1:]))
	case "remove":
		os.Exit(soleRemove(args[1:]))
	case "migrate":
		os.Exit(migrate(args[1:]))
	case "secret":
		// a secret for server.sessions: 48 random bytes, URL-safe
		fmt.Println(auth.Token(48))
		return
	case "fmt":
		os.Exit(formatFiles(args[1:]))
	case "fix":
		os.Exit(fixFiles(args[1:]))
	case "lsp":
		os.Exit(runLSP(os.Stdin, os.Stdout))
	case "check":
		need(args)
		if !checkFile(args[1]) {
			os.Exit(1)
		}
		fmt.Println("No problems found.")
	case "build":
		need(args)
		out := ""
		if len(args) >= 4 && args[2] == "-o" {
			out = args[3]
		}
		os.Exit(buildProgram(args[1], out))
	case "run":
		need(args)
		os.Exit(run(args[1]))
	case "test":
		need(args)
		os.Exit(test(args[1]))
	default:
		if strings.HasSuffix(args[0], ".lf") {
			os.Exit(run(args[0]))
		}
		fmt.Fprintf(os.Stderr, "I don't know the command '%s'.\n\n%s", args[0], help)
		os.Exit(2)
	}
}

func need(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Please give a .lf file, like: lf %s hello.lf\n", args[0])
		os.Exit(2)
	}
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't open %s: %v\n", path, err)
		os.Exit(2)
	}
	return string(b)
}

// newInterp makes an engine that can 'use' other .lf files next to the program.
func newInterp(path string) *lang.Interp {
	in := lang.NewInterp(os.Stdout)
	dir := filepath.Dir(path)
	in.Dir = dir
	in.Log = os.Stderr
	loadDotEnv(dir)
	in.ReadSource = func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		return string(b), err
	}
	return in
}

// checkFile runs lf check and prints any problems. Run, test and build all check first.
func checkFile(path string) bool {
	if err := verifyPackages(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	dir := filepath.Dir(path)
	problems := lang.Check(read(path), func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		return string(b), err
	})
	for _, p := range problems {
		if p.File == "" {
			p.File = path
		}
		fmt.Fprintln(os.Stderr, p.Error())
	}
	if len(problems) > 0 {
		s := "s"
		if len(problems) == 1 {
			s = ""
		}
		fmt.Fprintf(os.Stderr, "\n%d problem%s found. Nothing was run.\n", len(problems), s)
		return false
	}
	return true
}

func run(path string) int {
	if !checkFile(path) {
		return 1
	}
	in := newInterp(path)
	if err := in.Run(read(path)); err != nil {
		fmt.Fprintln(os.Stderr, describeError(path, err))
		return 1
	}
	return 0
}

func test(path string) int {
	if !checkFile(path) {
		return 1
	}
	in := newInterp(path)
	results, err := in.RunTests(read(path))
	if err != nil {
		fmt.Fprintln(os.Stderr, describeError(path, err))
		return 1
	}
	failed := 0
	for _, r := range results {
		if r.Passed {
			fmt.Printf("  ✓ %s\n", r.Name)
		} else {
			failed++
			fmt.Printf("  ✗ %s\n      %s\n", r.Name, r.Msg)
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", len(results)-failed, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// describeError writes a problem the same way lf check does: file, line N: message.
func describeError(path string, err error) string {
	switch e := err.(type) {
	case *lang.RuntimeError:
		return fmt.Sprintf("%s, line %d: %s", path, e.Line, e.Msg)
	case *lang.SyntaxError:
		return fmt.Sprintf("%s, line %d: %s", path, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %v", path, err)
}
