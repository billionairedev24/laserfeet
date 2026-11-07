package main

// Security advisories: known problems in package versions. The list lives in
// the Laser Feet repository (advisories/advisories.list) and is fetched with
// git like a package. lf audit checks sole.lock against it; lf get warns.
//
// The list's format, one block per advisory, blocks split by a blank line:
//
//	advisory LF-2026-0001
//	package github.com/someone/greetings
//	fixed v1.2.3            (versions before this are affected)
//	introduced v1.1.0       (optional: versions before this aren't)
//	summary A template could run visitor input as HTML.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const advisoriesRepo = "github.com/billionairedev24/laserfeet"

type advisory struct {
	ID, Package, Fixed, Introduced, Summary string
}

func parseAdvisories(text string) ([]advisory, error) {
	var out []advisory
	var cur *advisory
	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.ID == "" || cur.Package == "" || cur.Fixed == "" {
			return fmt.Errorf("advisory %q needs advisory, package and fixed lines", cur.ID)
		}
		out = append(out, *cur)
		cur = nil
		return nil
	}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "--") {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		value = strings.TrimSpace(value)
		if cur == nil {
			cur = &advisory{}
		}
		switch key {
		case "advisory":
			cur.ID = value
		case "package":
			cur.Package = value
		case "fixed":
			cur.Fixed = value
		case "introduced":
			cur.Introduced = value
		case "summary":
			cur.Summary = value
		default:
			return nil, fmt.Errorf("advisories, line %d: I don't know %q", n+1, key)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchAdvisories gets the newest list. LF_ADVISORIES names a file to use
// instead (for tests, or a machine without network access).
func fetchAdvisories() ([]advisory, error) {
	if f := os.Getenv("LF_ADVISORIES"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		return parseAdvisories(string(b))
	}
	url := repoURL(advisoriesRepo)
	version, commit, err := resolve(url, "main")
	if err != nil {
		return nil, err
	}
	tmp, err := download(url, version, commit)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	b, err := os.ReadFile(filepath.Join(tmp, "advisories", "advisories.list"))
	if err != nil {
		return nil, errors.New("the advisories list is missing")
	}
	return parseAdvisories(string(b))
}

// affecting lists the advisories that apply to the locked packages.
// "unsure" ones are for packages pinned to a branch or commit, whose version
// can't be compared.
func affecting(list []advisory, lock map[string]lockEntry) (sure, unsure []string) {
	for _, a := range list {
		e, ok := lock[a.Package]
		if !ok {
			continue
		}
		line := fmt.Sprintf("%s: %s %s — %s (fixed in %s)", a.ID, a.Package, e.Version, a.Summary, a.Fixed)
		have, ok1 := semver(e.Version)
		fixed, ok2 := semver(a.Fixed)
		if !ok1 || !ok2 {
			unsure = append(unsure, line)
			continue
		}
		if !newer(fixed, have) {
			continue // at or past the fix
		}
		if from, ok := semver(a.Introduced); ok && newer(from, have) {
			continue // from before the problem began
		}
		sure = append(sure, line)
	}
	sort.Strings(sure)
	sort.Strings(unsure)
	return sure, unsure
}

// reportAdvisories warns about installed packages with known problems. It
// never stops lf get: the packages are installed, and the warning says what
// to do.
func reportAdvisories(dir string, lock map[string]lockEntry) int {
	if len(lock) == 0 || os.Getenv("LF_SOLE_BASE") != "" && os.Getenv("LF_ADVISORIES") == "" {
		return 0 // nothing installed, or a test with its own repositories
	}
	list, err := fetchAdvisories()
	if err != nil {
		fmt.Printf("Couldn't check security advisories (%v). Run lf audit later.\n", err)
		return 0
	}
	sure, unsure := affecting(list, lock)
	for _, s := range sure {
		fmt.Println("Security problem: " + s + ". Update with lf get -u")
	}
	for _, s := range unsure {
		fmt.Println("Maybe affected (installed from a branch or commit): " + s)
	}
	return 0
}

// soleAudit is lf audit: it fails when an installed package has a known problem.
func soleAudit() int {
	lock, err := readLock(".")
	if err != nil {
		return soleFail(err)
	}
	list, err := fetchAdvisories()
	if err != nil {
		return soleFail(fmt.Errorf("couldn't get the security advisories: %v", err))
	}
	sure, unsure := affecting(list, lock)
	for _, s := range sure {
		fmt.Println("✗ " + s)
	}
	for _, s := range unsure {
		fmt.Println("? " + s + " (installed from a branch or commit: check it includes the fix)")
	}
	if len(sure) > 0 {
		fmt.Printf("\n%d known security problems. Update with lf get -u\n", len(sure))
		return 1
	}
	fmt.Printf("No known security problems in %d packages.\n", len(lock))
	return 0
}
