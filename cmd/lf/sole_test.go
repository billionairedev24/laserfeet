package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"laserfeet/lang"
)

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeRepo creates a git repository with the given files, committed and tagged.
func makeRepo(t *testing.T, dir, tag string, files map[string]string) {
	for name, text := range files {
		write(t, filepath.Join(dir, name), text)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		sh(t, dir, "git", "init", "-q", "-b", "main")
	}
	sh(t, dir, "git", "add", "-A")
	sh(t, dir, "git", "commit", "-q", "-m", tag)
	sh(t, dir, "git", "tag", "-f", tag)
}

func TestSolePackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	repos := t.TempDir()
	project := t.TempDir()
	t.Setenv("LF_SOLE_BASE", "file://"+repos)

	greet := filepath.Join(repos, "example.com", "acme", "greet")
	makeRepo(t, greet, "v1.0.0", map[string]string{
		"package.lf": "use \"words.lf\"\n\nbeam hello(name: Text) gives Text {\n    send \"{words.hi()}, {name}!\"\n}\n",
		"words.lf":   "beam hi() gives Text {\n    send \"Hello\"\n}\n",
		"README.md":  "not code, so it isn't installed",
	})
	makeRepo(t, greet, "v1.1.0", map[string]string{
		"words.lf": "beam hi() gives Text {\n    send \"Hi\"\n}\n",
	})
	tools := filepath.Join(repos, "example.com", "acme", "tools")
	makeRepo(t, tools, "v0.1.0", map[string]string{
		"textkit/package.lf": "beam shout(t: Text) gives Text {\n    send t.upper() + \"!\"\n}\n",
	})

	write(t, filepath.Join(project, "app.lf"), "use \"example.com/acme/greet\"\nuse \"example.com/acme/tools/textkit\"\n\nshow greet.hello(\"Ada\")\nshow textkit.shout(\"laser\")\n")
	old, _ := os.Getwd()
	os.Chdir(project)
	defer os.Chdir(old)

	// a version that isn't given gets the newest vX.Y.Z tag
	if code := soleGet([]string{"example.com/acme/greet"}); code != 0 {
		t.Fatal("lf get greet failed")
	}
	if code := soleGet([]string{"example.com/acme/tools/textkit@v0.1.0"}); code != 0 {
		t.Fatal("lf get textkit (a folder inside a repository) failed")
	}
	list, _ := os.ReadFile(listFile)
	if !strings.Contains(string(list), "need example.com/acme/greet v1.1.0") {
		t.Errorf("sole.list should record v1.1.0:\n%s", list)
	}
	if _, err := os.Stat(filepath.Join(lang.SoleDir, "example.com/acme/greet/README.md")); err == nil {
		t.Error("only .lf files should be installed")
	}

	runApp := func() (string, error) {
		var out bytes.Buffer
		if err := verifyPackages("app.lf"); err != nil {
			return "", err
		}
		src, _ := os.ReadFile("app.lf")
		read := func(p string) (string, error) {
			b, err := os.ReadFile(filepath.FromSlash(p))
			return string(b), err
		}
		if problems := lang.Check(string(src), read); len(problems) > 0 {
			return "", problems[0]
		}
		in := lang.NewInterp(&out)
		in.ReadSource = read
		err := in.Run(string(src))
		return out.String(), err
	}
	out, err := runApp()
	if err != nil || out != "Hi, Ada!\nLASER!\n" {
		t.Fatalf("running the app: %q %v", out, err)
	}

	// pinning an older version
	if code := soleGet([]string{"example.com/acme/greet@v1.0.0"}); code != 0 {
		t.Fatal("lf get greet@v1.0.0 failed")
	}
	if out, _ := runApp(); out != "Hello, Ada!\nLASER!\n" {
		t.Errorf("after pinning v1.0.0: %q", out)
	}

	// changing installed code is caught before anything runs
	pkg := filepath.Join(lang.SoleDir, "example.com/acme/greet/words.lf")
	write(t, pkg, "beam hi() gives Text {\n    send \"pwned\"\n}\n")
	if _, err := runApp(); err == nil || !strings.Contains(err.Error(), "was changed after it was installed") {
		t.Errorf("tampering must be caught, got %v", err)
	}
	// lf get restores it from the lock
	if code := soleGet(nil); code != 0 {
		t.Fatal("lf get (restore) failed")
	}
	if out, err := runApp(); err != nil || !strings.HasPrefix(out, "Hello, Ada!") {
		t.Errorf("after restoring: %q %v", out, err)
	}

	// if a tag is moved to different code, installing from the lock still gives
	// exactly the locked commit
	makeRepo(t, greet, "v1.0.0", map[string]string{"words.lf": "beam hi() gives Text {\n    send \"Sneaky\"\n}\n"})
	os.RemoveAll(lang.SoleDir)
	if code := soleGet(nil); code != 0 {
		t.Fatal("installing the locked commit failed")
	}
	if out, _ := runApp(); !strings.HasPrefix(out, "Hello, Ada!") {
		t.Errorf("a moved tag must not change locked code, got %q", out)
	}
	// and a package whose code no longer matches the lock is refused
	lock, _ := readLock(".")
	e := lock["example.com/acme/greet"]
	e.Hash = "sha256-0000000000000000000000000000000000000000000000000000000000000000"
	lock["example.com/acme/greet"] = e
	writeLock(".", lock)
	os.RemoveAll(lang.SoleDir)
	if code := soleGet(nil); code == 0 {
		t.Error("code that doesn't match sole.lock must be refused")
	}
	if _, err := os.Stat(filepath.Join(lang.SoleDir, "example.com/acme/greet")); err == nil {
		t.Error("nothing should be installed when the check fails")
	}

	// removing
	if code := soleRemove([]string{"example.com/acme/tools/textkit"}); code != 0 {
		t.Fatal("lf remove failed")
	}
	if list, _ := os.ReadFile(listFile); strings.Contains(string(list), "textkit") {
		t.Error("remove should take the package out of sole.list")
	}
}

func TestPackagesStayInTheirLane(t *testing.T) {
	for _, c := range []struct {
		ctx       lang.UseContext
		use, want string
	}{
		{lang.UseContext{Dir: ".sole/example.com/a/b", Root: ".sole/example.com/a/b"}, "../../../../app.lf", "only use its own files"},
		{lang.UseContext{}, ".sole/example.com/a/b/package.lf", "by its name"},
		{lang.UseContext{}, "../outside.lf", "inside your program's folder"},
	} {
		_, _, _, err := lang.ResolveUse(c.ctx, c.use)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("use %q: want %q, got %v", c.use, c.want, err)
		}
	}
	for _, bad := range []string{"-u/evil/x", "example.com/a", "example.com/../x/y", "Example.com/a/b/c.lf"} {
		if lang.IsPackagePath(bad) {
			t.Errorf("%q must not be accepted as a package path", bad)
		}
	}
}

// Packages can need packages: each is installed once, at the newest vX.Y.Z
// anything asks for; everything is locked and checked; and what nothing needs
// any more is taken away.
func TestPackagesThatNeedPackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	repos := t.TempDir()
	project := t.TempDir()
	t.Setenv("LF_SOLE_BASE", "file://"+repos)
	base := filepath.Join(repos, "example.com", "acme")

	// words: v1.0.0 says "hello", v1.2.0 also has "bye"
	makeRepo(t, filepath.Join(base, "words"), "v1.0.0", map[string]string{
		"package.lf": "beam hello() gives Text {\n    send \"hello\"\n}\n",
	})
	makeRepo(t, filepath.Join(base, "words"), "v1.2.0", map[string]string{
		"package.lf": "beam hello() gives Text {\n    send \"hello\"\n}\n\nbeam bye() gives Text {\n    send \"bye\"\n}\n",
	})
	// greet needs words v1.0.0; farewell needs words v1.2.0
	makeRepo(t, filepath.Join(base, "greet"), "v1.0.0", map[string]string{
		"sole.list":  "need example.com/acme/words v1.0.0\n",
		"package.lf": "use \"example.com/acme/words\"\n\nbeam greet(n: Text) gives Text {\n    send \"{words.hello()}, {n}\"\n}\n",
	})
	makeRepo(t, filepath.Join(base, "farewell"), "v1.0.0", map[string]string{
		"sole.list":  "need example.com/acme/words v1.2.0\n",
		"package.lf": "use \"example.com/acme/words\"\n\nbeam part(n: Text) gives Text {\n    send \"{words.bye()}, {n}\"\n}\n",
	})
	// clash needs words at a branch, which can't be compared with a version
	sh(t, filepath.Join(base, "words"), "git", "branch", "-f", "edge")
	makeRepo(t, filepath.Join(base, "clash"), "v1.0.0", map[string]string{
		"sole.list":  "need example.com/acme/words edge\n",
		"package.lf": "beam x() gives Int {\n    send 1\n}\n",
	})

	write(t, filepath.Join(project, "app.lf"), "use \"example.com/acme/greet\"\nuse \"example.com/acme/farewell\"\n\nshow greet.greet(\"Ada\")\nshow farewell.part(\"Ada\")\n")
	old, _ := os.Getwd()
	os.Chdir(project)
	defer os.Chdir(old)

	if code := soleGet([]string{"example.com/acme/greet@v1.0.0"}); code != 0 {
		t.Fatal("lf get greet failed")
	}
	lock, _ := readLock(".")
	if lock["example.com/acme/words"].Version != "v1.0.0" {
		t.Fatalf("greet's own need must be installed and locked: %v", lock)
	}
	if code := soleGet([]string{"example.com/acme/farewell@v1.0.0"}); code != 0 {
		t.Fatal("lf get farewell failed")
	}
	lock, _ = readLock(".")
	if lock["example.com/acme/words"].Version != "v1.2.0" {
		t.Fatalf("the newer of v1.0.0 and v1.2.0 must be used: %v", lock)
	}
	if list, _ := os.ReadFile(listFile); strings.Contains(string(list), "words") {
		t.Errorf("sole.list lists only what the project itself needs:\n%s", list)
	}

	run := func() (string, error) {
		if err := verifyPackages("app.lf"); err != nil {
			return "", err
		}
		src, _ := os.ReadFile("app.lf")
		read := func(p string) (string, error) {
			b, err := os.ReadFile(filepath.FromSlash(p))
			return string(b), err
		}
		if problems := lang.Check(string(src), read); len(problems) > 0 {
			return "", problems[0]
		}
		var out bytes.Buffer
		in := lang.NewInterp(&out)
		in.ReadSource = read
		err := in.Run(string(src))
		return out.String(), err
	}
	if out, err := run(); err != nil || out != "hello, Ada\nbye, Ada\n" {
		t.Fatalf("running: %q %v", out, err)
	}

	// a package's own list is covered by the lock: changing it is caught
	write(t, filepath.Join(lang.SoleDir, "example.com/acme/greet/sole.list"), "need example.com/acme/clash v1.0.0\n")
	if err := verifyPackages("app.lf"); err == nil {
		t.Error("changing an installed package's sole.list must be caught")
	}
	if code := soleGet(nil); code != 0 {
		t.Fatal("lf get (restore) failed")
	}

	// removing farewell: words stays (greet still needs it), at the version
	// installed, since a newer one still does for greet
	if code := soleRemove([]string{"example.com/acme/farewell"}); code != 0 {
		t.Fatal("lf remove farewell failed")
	}
	lock, _ = readLock(".")
	if _, ok := lock["example.com/acme/farewell"]; ok {
		t.Error("farewell must leave the lock")
	}
	if _, ok := lock["example.com/acme/words"]; !ok {
		t.Error("words is still needed by greet")
	}
	// removing greet: nothing needs words, so it goes too
	if code := soleRemove([]string{"example.com/acme/greet"}); code != 0 {
		t.Fatal("lf remove greet failed")
	}
	lock, _ = readLock(".")
	if len(lock) != 0 {
		t.Errorf("nothing is needed any more, but the lock has %v", lock)
	}
	if _, err := os.Stat(filepath.Join(lang.SoleDir, "example.com/acme/words")); err == nil {
		t.Error("words should be uninstalled")
	}

	// versions that can't be compared are a conflict, explained
	soleGet([]string{"example.com/acme/greet@v1.0.0"})
	if code := soleGet([]string{"example.com/acme/clash@v1.0.0"}); code == 0 {
		t.Error("words v1.0.0 and words edge must conflict")
	}
	if list, _ := os.ReadFile(listFile); strings.Contains(string(list), "clash") {
		t.Error("a failed lf get must not change sole.list")
	}
}
