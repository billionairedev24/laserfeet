// Command lf runs Laser Feet programs.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"laserfeet/auth"
	"laserfeet/lang"
)

var version = "dev" // set from the VERSION file by scripts/build.sh

var help = `Laser Feet ` + version + `

Usage:
  lf new <name>        create a starter project
  lf run <file.lf>     run a program
  lf test [paths]      run the test blocks in files, or every file under folders
                       (default: this folder); --cover shows which lines ran
  lf check <file.lf>   find type mistakes without running anything
  lf migrate [folder]  apply new .sql migrations to DATABASE_URL
  lf secret            print a new random secret (for server.sessions)
  lf fmt [files]       lay out code the standard way (--check: only report)
  lf fix [files]       update code written for an older Laser Feet
  lf get <package>     add a package, like: lf get github.com/someone/greetings@v1.2.0
  lf get               install the packages in sole.list, checked against sole.lock
  lf get -u [package]  update packages to their newest version with the same first number
  lf remove <package>  stop using a package
  lf audit             check installed packages against known security problems
  lf doc [paths]       documentation from the comments above beams, objects and
                       choices (Markdown; --html file.html for a web page)
  lf try               type Laser Feet and see what it gives, line by line
  lf lsp               start the language server for editors (VS Code uses this)
  lf build <file.lf>   make a standalone program (add -o name to pick its name)
  lf version           show the version
  lf help              show this help

You can also just type: lf <file.lf>
`

func main() {
	// Laser Feet programs make many small values; collecting a little less
	// often is about 15% faster for the same peak memory on typical work.
	// GOGC set by the person running the program still wins.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
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
	case "audit":
		os.Exit(soleAudit())
	case "doc":
		os.Exit(docCommand(args[1:]))
	case "try":
		os.Exit(tryCommand(os.Stdin, os.Stdout))
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
		os.Exit(testCommand(args[1:]))
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

// testCommand runs lf test: files, or every file with tests under folders
// (the current folder if none is named), optionally with coverage.
func testCommand(args []string) int {
	cover := false
	var paths []string
	for _, a := range args {
		switch a {
		case "--cover", "-cover":
			cover = true
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "I couldn't open %s: %v\n", p, err)
			return 1
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		found, err := filesWithTests(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		files = append(files, found...)
	}
	if len(files) == 0 {
		fmt.Println("No tests found. Tests are 'test \"name\" { ... }' blocks in .lf files.")
		return 0
	}
	passed, failed, broken := 0, 0, 0
	for _, f := range files {
		if len(files) > 1 {
			fmt.Printf("%s\n", f)
		}
		p, n, ok := test(f, cover)
		passed, failed = passed+p, failed+n
		if !ok {
			broken++
		}
	}
	if len(files) > 1 {
		fmt.Printf("\n%d files: %d passed, %d failed", len(files), passed, failed)
		if broken == 1 {
			fmt.Print(", 1 file couldn't run")
		} else if broken > 1 {
			fmt.Printf(", %d files couldn't run", broken)
		}
		fmt.Println()
	}
	if failed > 0 || broken > 0 {
		return 1
	}
	return 0
}

// filesWithTests finds the .lf files under dir that have test blocks,
// leaving out installed packages and hidden folders.
func filesWithTests(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == lang.SoleDir || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".lf") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if prog, perr := lang.Parse(string(src)); perr == nil {
			for _, s := range prog.Stmts {
				if _, ok := s.(*lang.TestStmt); ok {
					out = append(out, p)
					break
				}
			}
		}
		return nil
	})
	return out, err
}

// test runs one file's tests. It reports how many passed and failed, and
// whether the file could run at all.
func test(path string, cover bool) (int, int, bool) {
	if !checkFile(path) {
		return 0, 0, false
	}
	in := newInterp(path)
	if cover {
		in.Cover = lang.NewCoverage()
	}
	results, err := in.RunTests(read(path))
	if err != nil {
		fmt.Fprintln(os.Stderr, describeError(path, err))
		return 0, 0, false
	}
	failed := 0
	for _, r := range results {
		if r.Passed {
			fmt.Printf("  ✓ %s\n", r.Name)
		} else {
			failed++
			fmt.Printf("  ✗ %s\n      %s\n", r.Name, strings.ReplaceAll(r.Msg, "\n", "\n      "))
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", len(results)-failed, failed)
	if cover {
		for _, fc := range in.Cover.Report() {
			name := placeOf(path, fc.File)
			pct := 100.0
			if fc.Total > 0 {
				pct = 100 * float64(fc.Run) / float64(fc.Total)
			}
			fmt.Printf("coverage: %s %.0f%% of %d statements", name, pct, fc.Total)
			if len(fc.Missed) > 0 {
				fmt.Printf("; not run: %s", lineRanges(fc.Missed))
			}
			fmt.Println()
		}
	}
	return len(results) - failed, failed, true
}

// lineRanges writes sorted line numbers briefly: "line 4" or "lines 4, 9-12".
func lineRanges(lines []int) string {
	var parts []string
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(lines[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", lines[i], lines[j]))
		}
		i = j + 1
	}
	word := "lines "
	if len(lines) == 1 {
		word = "line "
	}
	return word + strings.Join(parts, ", ")
}

// placeOf names a file a problem happened in: the program's own, a file it
// uses (next to it), or a package (as written in use).
func placeOf(main, file string) string {
	switch {
	case file == "":
		return main
	case lang.IsPackagePath(file):
		return file
	}
	return filepath.Join(filepath.Dir(main), filepath.FromSlash(file))
}

// describeError writes a problem the same way lf check does: file, line N: message.
func describeError(path string, err error) string {
	switch e := err.(type) {
	case *lang.RuntimeError:
		return fmt.Sprintf("%s, line %d: %s", placeOf(path, e.File), e.Line, e.Msg) + e.TraceText(func(f string) string { return placeOf(path, f) })
	case *lang.SyntaxError:
		return fmt.Sprintf("%s, line %d: %s", path, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %v", path, err)
}
