package lang

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Where `use` finds code:
//
//	use "helpers.lf"                       a file, relative to the file that uses it
//	use "github.com/someone/greetings"     a package installed by `lf get`
//
// Installed packages live in the project's .sole folder. Each package's entry
// file is package.lf. Files can never reach outside the project, and a package
// can only use its own files and other packages.

// SoleDir is the folder, inside a project, where lf get installs packages.
const SoleDir = ".sole"

// PackageEntry is the file that `use "host/owner/repo"` loads from a package.
const PackageEntry = "package.lf"

var packagePath = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+(/[A-Za-z0-9_.-]+){2,}$`)

// IsPackagePath reports whether a use path names a package, like github.com/someone/greetings.
func IsPackagePath(p string) bool {
	return !strings.HasSuffix(p, ".lf") && packagePath.MatchString(p) && !strings.Contains(p, "..")
}

// UseContext is where a file being loaded lives: its folder, and the root of the
// package it belongs to ("" for the project's own files).
type UseContext struct {
	Dir  string
	Root string
}

// ResolveUse works out which file `use p` means, from a file in ctx. It returns the
// file's path inside the project (the key used to read it), the name the code
// reaches it by, and the context for loading that file.
func ResolveUse(ctx UseContext, p string) (key, name string, next UseContext, err error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if IsPackagePath(p) {
		root := SoleDir + "/" + p
		key = root + "/" + PackageEntry
		last := p[strings.LastIndex(p, "/")+1:]
		name = strings.NewReplacer("-", "_", ".", "_").Replace(last)
		if !validIdent(name) {
			return "", "", next, fmt.Errorf("the package's last part, %q, must start with a letter", last)
		}
		return key, name, UseContext{Dir: root, Root: root}, nil
	}
	if !strings.HasSuffix(p, ".lf") {
		return "", "", next, errors.New("use a .lf file, like use \"helpers.lf\", or a package, like use \"github.com/someone/greetings\"")
	}
	if path.IsAbs(p) {
		return "", "", next, errors.New("files must be inside your program's folder")
	}
	key = path.Clean(path.Join(ctx.Dir, p))
	if key == ".." || strings.HasPrefix(key, "../") {
		return "", "", next, errors.New("files must be inside your program's folder")
	}
	if ctx.Root != "" && !strings.HasPrefix(key, ctx.Root+"/") {
		return "", "", next, errors.New("a package can only use its own files and other packages")
	}
	if ctx.Root == "" && (key == SoleDir || strings.HasPrefix(key, SoleDir+"/")) {
		return "", "", next, errors.New("use a package by its name, like use \"github.com/someone/greetings\", not through the .sole folder")
	}
	name = strings.TrimSuffix(path.Base(key), ".lf")
	return key, name, UseContext{Dir: path.Dir(key), Root: ctx.Root}, nil
}

func validIdent(s string) bool {
	if s == "" || !isLetter(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !isLetter(r) && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return !keywords[s]
}

// IsName reports whether s can be a name in Laser Feet code.
func IsName(s string) bool { return validIdent(s) }
