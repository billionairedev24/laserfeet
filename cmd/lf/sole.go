package main

// sole: Laser Feet's package manager. Packages are git repositories (or folders
// inside them) whose entry file is package.lf.
//
//   lf get github.com/someone/greetings@v1.2.0   add or update a package
//   lf get                                       install everything in sole.list
//   lf remove github.com/someone/greetings       stop using a package
//
// sole.list says which packages a project needs; sole.lock records the exact
// commit and a SHA-256 of every package's code. Packages are checked against the
// lock when they are installed and every time the program runs, so code can't
// change underneath you. Packages contain only .lf files and never run anything
// when they're installed.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"laserfeet/lang"
)

const (
	listFile = "sole.list"
	lockFile = "sole.lock"
)

var (
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverTag      = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
)

type pkgNeed struct {
	Path    string
	Version string
}

type lockEntry struct {
	Version string
	Commit  string
	Hash    string
}

// ---------- the list and the lock ----------

func readList(dir string) ([]pkgNeed, error) {
	f, err := os.Open(filepath.Join(dir, listFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pkgNeed
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 || parts[0] != "need" || !lang.IsPackagePath(parts[1]) || !versionPattern.MatchString(parts[2]) {
			return nil, fmt.Errorf("%s, line %d: expected 'need <package> <version>', like: need github.com/someone/greetings v1.2.0", listFile, n)
		}
		out = append(out, pkgNeed{parts[1], parts[2]})
	}
	return out, sc.Err()
}

func writeList(dir string, needs []pkgNeed) error {
	sort.Slice(needs, func(i, j int) bool { return needs[i].Path < needs[j].Path })
	var b strings.Builder
	b.WriteString("-- Packages this project uses. Change them with: lf get <package>@<version> or lf remove <package>\n")
	for _, n := range needs {
		fmt.Fprintf(&b, "need %s %s\n", n.Path, n.Version)
	}
	return os.WriteFile(filepath.Join(dir, listFile), []byte(b.String()), 0o644)
}

func readLock(dir string) (map[string]lockEntry, error) {
	out := map[string]lockEntry{}
	data, err := os.ReadFile(filepath.Join(dir, lockFile))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		p := strings.Fields(line)
		if len(p) != 4 || !commitPattern.MatchString(p[2]) || !strings.HasPrefix(p[3], "sha256-") {
			return nil, fmt.Errorf("%s, line %d is damaged. Delete %s and run lf get to make a new one", lockFile, i+1, lockFile)
		}
		out[p[0]] = lockEntry{p[1], p[2], p[3]}
	}
	return out, nil
}

func writeLock(dir string, lock map[string]lockEntry) error {
	var paths []string
	for p := range lock {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("-- Written by lf get. Don't edit: every package is checked against these lines.\n")
	b.WriteString("-- package  version  commit  sha256 of its code\n")
	for _, p := range paths {
		e := lock[p]
		fmt.Fprintf(&b, "%s %s %s %s\n", p, e.Version, e.Commit, e.Hash)
	}
	return os.WriteFile(filepath.Join(dir, lockFile), []byte(b.String()), 0o644)
}

// ---------- hashing ----------

// hashPackage is a SHA-256 over every .lf file in a package: its path and contents.
func hashPackage(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link, which packages may not contain", p)
		}
		if !d.IsDir() && strings.HasSuffix(p, ".lf") {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s %s\n", f, hex.EncodeToString(sum[:]))
	}
	return "sha256-" + hex.EncodeToString(h.Sum(nil)), nil
}

// ---------- git ----------

// splitPackage turns github.com/owner/repo/sub/dir into the repository and the folder inside it.
func splitPackage(p string) (repo, sub string) {
	parts := strings.Split(p, "/")
	return strings.Join(parts[:3], "/"), strings.Join(parts[3:], "/")
}

func repoURL(repo string) string {
	if base := os.Getenv("LF_SOLE_BASE"); base != "" { // for tests, with local repositories
		return strings.TrimRight(base, "/") + "/" + repo
	}
	return "https://" + repo + ".git"
}

// git runs git safely: only HTTPS is allowed (and local folders only in tests), it
// never asks for a password, and nothing from the repository can run.
func git(args ...string) (string, error) {
	base := []string{
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
		"-c", "core.hooksPath=/dev/null", "-c", "advice.detachedHead=false",
	}
	if os.Getenv("LF_SOLE_BASE") != "" {
		base = append(base, "-c", "protocol.file.allow=always")
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if errors.Is(err, exec.ErrNotFound) {
			msg = "git isn't installed. Install git, then try again"
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// resolve finds the commit for a version (a tag, a branch, or a commit), or the newest
// vX.Y.Z tag when no version is given.
func resolve(url, version string) (string, string, error) {
	if commitPattern.MatchString(version) {
		return version, version, nil
	}
	out, err := git("ls-remote", "--", url)
	if err != nil {
		return "", "", fmt.Errorf("couldn't reach %s: %v", url, err)
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			refs[f[1]] = f[0]
		}
	}
	if version == "" {
		best, bestKey := "", [3]int{-1, -1, -1}
		for ref := range refs {
			tag := strings.TrimPrefix(ref, "refs/tags/")
			if m := semverTag.FindStringSubmatch(tag); m != nil {
				var k [3]int
				for i := range k {
					k[i], _ = strconv.Atoi(m[i+1])
				}
				if k[0] > bestKey[0] || k[0] == bestKey[0] && (k[1] > bestKey[1] || k[1] == bestKey[1] && k[2] > bestKey[2]) {
					best, bestKey = tag, k
				}
			}
		}
		if best != "" {
			version = best
		} else if c, ok := refs["HEAD"]; ok {
			return "HEAD", c, nil
		} else {
			return "", "", errors.New("the repository has no commits")
		}
	}
	for _, ref := range []string{"refs/tags/" + version + "^{}", "refs/tags/" + version, "refs/heads/" + version} {
		if c, ok := refs[ref]; ok {
			return version, c, nil
		}
	}
	return "", "", fmt.Errorf("%s has no tag or branch called %s", url, version)
}

// download fetches one commit into a new temporary folder.
func download(url, version, commit string) (string, error) {
	tmp, err := os.MkdirTemp("", "sole-*")
	if err != nil {
		return "", err
	}
	if _, err := git("init", "-q", tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	// Fetch exactly the commit we want. Some servers only hand out named refs, so
	// fall back to the tag or branch, and then insist it is still that commit.
	if _, err := git("-C", tmp, "fetch", "-q", "--depth", "1", "--", url, commit); err != nil {
		ref := version
		if version == commit || version == "HEAD" {
			ref = "HEAD"
		}
		if _, err := git("-C", tmp, "fetch", "-q", "--depth", "1", "--", url, ref); err != nil {
			os.RemoveAll(tmp)
			return "", fmt.Errorf("couldn't download %s %s: %v", url, version, err)
		}
	}
	if _, err := git("-C", tmp, "checkout", "-q", "FETCH_HEAD"); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	got, err := git("-C", tmp, "rev-parse", "HEAD")
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if strings.TrimSpace(got) != commit {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("%s now points to a different commit than expected (%s instead of %s). If this change is expected, run lf get with the version again to accept it", version, strings.TrimSpace(got)[:12], commit[:12])
	}
	return tmp, nil
}

// install puts one package into .sole, checking it against want ("" to accept a new one).
func install(dir string, n pkgNeed, locked *lockEntry) (lockEntry, error) {
	repo, sub := splitPackage(n.Path)
	url := repoURL(repo)
	var version, commit string
	if locked != nil && locked.Version == n.Version {
		version, commit = locked.Version, locked.Commit
	} else {
		var err error
		version, commit, err = resolve(url, n.Version)
		if err != nil {
			return lockEntry{}, err
		}
	}
	tmp, err := download(url, version, commit)
	if err != nil {
		return lockEntry{}, err
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, filepath.FromSlash(sub))
	if _, err := os.Stat(filepath.Join(src, lang.PackageEntry)); err != nil {
		return lockEntry{}, fmt.Errorf("%s has no %s, so it isn't a Laser Feet package", n.Path, lang.PackageEntry)
	}
	os.RemoveAll(filepath.Join(src, ".git"))
	hash, err := hashPackage(src)
	if err != nil {
		return lockEntry{}, err
	}
	if locked != nil && locked.Version == version && locked.Hash != hash {
		return lockEntry{}, fmt.Errorf("%s %s doesn't match sole.lock: its code has changed since it was locked. Nothing was installed. If you trust the change, run: lf get %s@%s", n.Path, version, n.Path, version)
	}
	dest := filepath.Join(dir, lang.SoleDir, filepath.FromSlash(n.Path))
	if err := os.RemoveAll(dest); err != nil {
		return lockEntry{}, err
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		if !strings.HasSuffix(p, ".lf") {
			return nil // packages are code only
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, rel), data, 0o644)
	})
	if err != nil {
		return lockEntry{}, err
	}
	// .sole is rebuilt from sole.list and sole.lock, so it doesn't belong in git
	os.WriteFile(filepath.Join(dir, lang.SoleDir, ".gitignore"), []byte("*\n"), 0o644)
	return lockEntry{Version: version, Commit: commit, Hash: hash}, nil
}

// ---------- commands ----------

func soleGet(args []string) int {
	dir := "."
	needs, err := readList(dir)
	if err != nil {
		return soleFail(err)
	}
	lock, err := readLock(dir)
	if err != nil {
		return soleFail(err)
	}
	if len(args) > 0 { // add or update one package
		p, version, _ := strings.Cut(args[0], "@")
		if !lang.IsPackagePath(p) {
			return soleFail(fmt.Errorf("%q isn't a package path. Packages look like github.com/someone/greetings", p))
		}
		if version != "" && !versionPattern.MatchString(version) {
			return soleFail(fmt.Errorf("%q isn't a valid version", version))
		}
		e, err := install(dir, pkgNeed{p, version}, nil)
		if err != nil {
			return soleFail(err)
		}
		found := false
		for i := range needs {
			if needs[i].Path == p {
				needs[i].Version, found = e.Version, true
			}
		}
		if !found {
			needs = append(needs, pkgNeed{p, e.Version})
		}
		lock[p] = e
		if err := writeList(dir, needs); err != nil {
			return soleFail(err)
		}
		if err := writeLock(dir, lock); err != nil {
			return soleFail(err)
		}
		fmt.Printf("Added %s %s (commit %s)\nUse it with: use \"%s\"\n", p, e.Version, e.Commit[:12], p)
		return 0
	}
	if len(needs) == 0 {
		fmt.Println("No packages to install. Add one with: lf get github.com/someone/greetings")
		return 0
	}
	changed := false
	for _, n := range needs {
		var locked *lockEntry
		if e, ok := lock[n.Path]; ok {
			locked = &e
			if h, err := hashPackage(filepath.Join(dir, lang.SoleDir, filepath.FromSlash(n.Path))); err == nil && h == e.Hash && e.Version == n.Version {
				fmt.Printf("  ✓ %s %s\n", n.Path, n.Version)
				continue
			}
		}
		e, err := install(dir, n, locked)
		if err != nil {
			return soleFail(err)
		}
		lock[n.Path] = e
		changed = true
		fmt.Printf("  ✓ %s %s (installed)\n", n.Path, e.Version)
	}
	for p := range lock { // forget packages no longer listed
		listed := false
		for _, n := range needs {
			listed = listed || n.Path == p
		}
		if !listed {
			delete(lock, p)
			changed = true
		}
	}
	if changed {
		if err := writeLock(dir, lock); err != nil {
			return soleFail(err)
		}
	}
	return 0
}

func soleRemove(args []string) int {
	if len(args) == 0 {
		return soleFail(errors.New("say which package to remove, like: lf remove github.com/someone/greetings"))
	}
	dir := "."
	needs, err := readList(dir)
	if err != nil {
		return soleFail(err)
	}
	lock, err := readLock(dir)
	if err != nil {
		return soleFail(err)
	}
	var kept []pkgNeed
	for _, n := range needs {
		if n.Path != args[0] {
			kept = append(kept, n)
		}
	}
	if len(kept) == len(needs) {
		return soleFail(fmt.Errorf("%s isn't in %s", args[0], listFile))
	}
	delete(lock, args[0])
	os.RemoveAll(filepath.Join(dir, lang.SoleDir, filepath.FromSlash(args[0])))
	if err := writeList(dir, kept); err != nil {
		return soleFail(err)
	}
	if err := writeLock(dir, lock); err != nil {
		return soleFail(err)
	}
	fmt.Printf("Removed %s\n", args[0])
	return 0
}

func soleFail(err error) int {
	fmt.Fprintln(os.Stderr, "lf get:", err)
	return 1
}

// verifyPackages checks installed packages against sole.lock before a program runs.
func verifyPackages(programPath string) error {
	dir := filepath.Dir(programPath)
	lock, err := readLock(dir)
	if err != nil {
		return err
	}
	for p, e := range lock {
		root := filepath.Join(dir, lang.SoleDir, filepath.FromSlash(p))
		if _, err := os.Stat(root); err != nil {
			return fmt.Errorf("package %s isn't installed. Run: lf get", p)
		}
		h, err := hashPackage(root)
		if err != nil {
			return err
		}
		if h != e.Hash {
			return fmt.Errorf("package %s was changed after it was installed (its SHA-256 doesn't match sole.lock). Run lf get to restore it", p)
		}
	}
	return nil
}
