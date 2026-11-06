package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"laserfeet/lang"
)

// lfFiles lists the .lf files named, looking inside folders (but not in .sole,
// .git or other hidden folders). No names means the current folder.
func lfFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var out []string
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, fmt.Errorf("I couldn't find %s", a)
		}
		if !info.IsDir() {
			out = append(out, a)
			continue
		}
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != a && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".lf") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// writeKeepingMode replaces a file's contents without changing its permissions.
func writeKeepingMode(path, text string) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(text), mode)
}

// formatFiles is lf fmt. With --check it changes nothing and fails if any file
// isn't laid out the standard way (for CI).
func formatFiles(args []string) int {
	check := false
	var paths []string
	for _, a := range args {
		if a == "--check" {
			check = true
		} else {
			paths = append(paths, a)
		}
	}
	files, err := lfFiles(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	bad := 0
	for _, f := range files {
		src := read(f)
		out, err := lang.Format(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			bad++
			continue
		}
		if out == src {
			continue
		}
		if check {
			fmt.Fprintf(os.Stderr, "%s is not formatted (run: lf fmt %s)\n", f, f)
			bad++
			continue
		}
		if err := writeKeepingMode(f, out); err != nil {
			fmt.Fprintf(os.Stderr, "I couldn't write %s: %v\n", f, err)
			return 1
		}
		fmt.Println("formatted " + f)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

// db.transaction() as tx {  →  transaction with db as tx {
var oldTransaction = regexp.MustCompile(`(?m)^([ \t]*)([A-Za-z_][A-Za-z0-9_.]*)\.transaction\(\) as ([A-Za-z_][A-Za-z0-9_]*) \{`)

// fixSource brings code written for an older Laser Feet up to date, keeping
// what it does exactly the same.
func fixSource(src string, readUse func(string) (string, error)) (string, int, error) {
	changes := 0
	for round := 0; round < 5; round++ { // each round can reveal the next (rare)
		edits, err := lang.FixEdits(src, readUse)
		if err != nil {
			return "", 0, err
		}
		if len(edits) == 0 {
			break
		}
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].At > edits[j].At })
		rs := []rune(src)
		for _, e := range edits {
			rs = append(rs[:e.At], append([]rune(e.Text), rs[e.At:]...)...)
		}
		src = string(rs)
		changes += len(edits)
	}
	src = oldTransaction.ReplaceAllString(src, "${1}transaction with ${2} as ${3} {")
	if out, err := lang.Format(src); err == nil {
		src = out
	}
	return src, changes, nil
}

// fixFiles is lf fix.
func fixFiles(args []string) int {
	files, err := lfFiles(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	failed := false
	for _, f := range files {
		src := read(f)
		dir := filepath.Dir(f)
		out, n, err := fixSource(src, func(p string) (string, error) {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
			return string(b), err
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			failed = true
			continue
		}
		if out == src {
			continue
		}
		if err := writeKeepingMode(f, out); err != nil {
			fmt.Fprintf(os.Stderr, "I couldn't write %s: %v\n", f, err)
			return 1
		}
		if n > 0 {
			fmt.Printf("updated %s (%d change%s)\n", f, n, plural(n))
		} else {
			fmt.Printf("formatted %s\n", f)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
